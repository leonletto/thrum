package contextpoll

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// PollerConfig is the immutable runtime configuration for a Poller. Construct
// via NewPoller; zero values for any field fall back to documented defaults.
//
// The threshold fields (WarnThreshold, AutoThreshold) are required for the
// engine to decide which callback to fire. The spec § interface contract did
// not list them on PollerConfig — they're added here because the threshold
// logic lives inside the Poller (the callbacks describe WHAT to do; the
// engine decides WHEN). The wiring at daemon init reads them from
// config.RestartConfig accessors (WarnThresholdValue / AutoThresholdValue).
type PollerConfig struct {
	// PollInterval is the ticker cadence. Default 30s.
	PollInterval time.Duration

	// PreFireWait is the time between the auto-tier pre-fire nudge and the
	// force-fire callback. Q3 (LOCKED) fixed this at 3 minutes; setting
	// shorter values is supported for tests.
	PreFireWait time.Duration

	// InFlightMaxWait is the upper bound on the "restart-in-flight" guard
	// — after this much wall-clock without the agent's session resetting,
	// the Poller clears the in-flight flag so callbacks can fire again.
	// Backstops a hung restart that never reaches PostRestart. Default 5m.
	InFlightMaxWait time.Duration

	// WarnThreshold is the % at which OnWarn fires (default 70).
	WarnThreshold int

	// AutoThreshold is the % at which OnPreFire fires (default 80). The
	// OnFire callback fires PreFireWait after OnPreFire.
	AutoThreshold int
}

// AgentEnrollment is the per-agent input to Poller.Enroll. The daemon resolves
// the transcript path at enrollment time using restart.FindSessionJSONL /
// FindLatestJSONLForCwd; the Poller treats TranscriptPath as opaque and just
// hands it to the matching Parser.
//
// TranscriptPath may be empty at enroll-time if the agent's session has not
// yet produced a transcript file. The Poller silently skips agents with an
// empty path; the wiring re-Enrolls on identity refresh with the resolved
// path once it appears.
type AgentEnrollment struct {
	TranscriptPath string
	AgentPID       int
	AgentCwd       string
	Runtime        string
	SessionID      string
}

// Callback signatures invoked when thresholds are crossed. Callbacks run on
// the Poller's goroutine, so they should not block on long I/O — they should
// dispatch onto their own goroutine or hand work to the daemon's existing
// task plumbing if they need to do significant work.
type (
	WarnCallback    func(ctx context.Context, agentName string, usage ContextUsage)
	PreFireCallback func(ctx context.Context, agentName string, usage ContextUsage)
	FireCallback    func(ctx context.Context, agentName string, usage ContextUsage)
)

// agentPollState tracks one enrolled agent's threshold debounce state. All
// access guarded by Poller.mu.
type agentPollState struct {
	enrollment AgentEnrollment

	// parser is the first registered Parser whose Matches returned true for
	// the agent's transcript. Sticky-cached to avoid re-probing on every
	// poll; cleared on PostRestart when a fresh session may write a new
	// format.
	parser Parser

	lastUsage ContextUsage

	warnFired         bool
	preFired          bool
	preFiredAt        time.Time
	restartInFlight   bool
	restartInFlightAt time.Time
}

// Poller is the context-usage polling engine. Construct via NewPoller; set
// callbacks with OnWarn / OnPreFire / OnFire; add parsers with RegisterParser;
// enroll agents with Enroll; start the loop with Run.
type Poller struct {
	cfg PollerConfig

	mu      sync.Mutex
	parsers []Parser
	agents  map[string]*agentPollState

	onWarn    WarnCallback
	onPreFire PreFireCallback
	onFire    FireCallback

	// nowFn is the clock source. Tests override to drive the threshold logic
	// without spinning the real wall clock. Defaults to time.Now.
	nowFn func() time.Time
}

// NewPoller constructs a Poller with the given config; zero-valued config
// fields fall back to documented defaults (PollInterval 30s, PreFireWait 3m,
// InFlightMaxWait 5m, WarnThreshold 70, AutoThreshold 80).
func NewPoller(cfg PollerConfig) *Poller {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.PreFireWait <= 0 {
		cfg.PreFireWait = 3 * time.Minute
	}
	if cfg.InFlightMaxWait <= 0 {
		cfg.InFlightMaxWait = 5 * time.Minute
	}
	if cfg.WarnThreshold <= 0 {
		cfg.WarnThreshold = 70
	}
	if cfg.AutoThreshold <= 0 {
		cfg.AutoThreshold = 80
	}
	return &Poller{
		cfg:    cfg,
		agents: make(map[string]*agentPollState),
		nowFn:  time.Now,
	}
}

// OnWarn registers the callback fired at WarnThreshold. Idempotent overwrite.
func (p *Poller) OnWarn(cb WarnCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onWarn = cb
}

// OnPreFire registers the pre-fire callback fired at AutoThreshold.
func (p *Poller) OnPreFire(cb PreFireCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onPreFire = cb
}

// OnFire registers the force-fire callback. Fires PreFireWait after the
// pre-fire callback (Q3-LOCKED at 3 minutes by default).
func (p *Poller) OnFire(cb FireCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onFire = cb
}

// RegisterParser appends a parser to the dispatch list. Parser.Matches is
// invoked in registration order at first poll for each agent; the first true
// wins and is sticky-cached until PostRestart.
func (p *Poller) RegisterParser(parser Parser) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.parsers = append(p.parsers, parser)
}

// Enroll adds an agent to the polling table. Safe to call multiple times for
// the same agent; the most recent enrollment wins (the prior state's threshold
// flags are dropped, since a fresh transcript path likely means a fresh
// session).
func (p *Poller) Enroll(agentName string, e AgentEnrollment) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.agents[agentName] = &agentPollState{enrollment: e}
}

// Unenroll removes an agent from the polling table. No-op for unknown names.
func (p *Poller) Unenroll(agentName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.agents, agentName)
}

// PostRestart clears all threshold debounce state for the named agent. Called
// by the daemon when a session reset is observed (identity refresh, new
// transcript path, post-force-fire reconnect). Also clears the sticky parser
// choice so a runtime swap mid-session is handled cleanly.
func (p *Poller) PostRestart(agentName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.agents[agentName]
	if !ok {
		return
	}
	state.warnFired = false
	state.preFired = false
	state.preFiredAt = time.Time{}
	state.restartInFlight = false
	state.restartInFlightAt = time.Time{}
	state.parser = nil
	state.lastUsage = ContextUsage{}
}

// ContextUsageFor implements ContextProvider. Returns the most recently
// cached ContextUsage for agentName, or (ContextUsage{}, false) if the agent
// is not enrolled or has not yet produced a successful parse.
func (p *Poller) ContextUsageFor(agentName string) (ContextUsage, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.agents[agentName]
	if !ok || state.lastUsage.ParserVersion == "" {
		return ContextUsage{}, false
	}
	return state.lastUsage, true
}

// Run is the polling loop. Exits cleanly when ctx is cancelled. The interval
// argument overrides PollerConfig.PollInterval — tests pass a short interval;
// the daemon passes the configured value (typically 30s).
func (p *Poller) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = p.cfg.PollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	slog.Info("[contextpoll] started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			slog.Info("[contextpoll] stopped", "reason", ctx.Err())
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

// pollOnce executes a single polling cycle. Exposed (lowercase) for direct
// invocation from tests; the Run loop calls it on each ticker tick.
//
// The cycle takes a snapshot of enrolled agents and current callbacks under
// the lock, then runs parser work + callback invocations outside the lock so
// a slow parser does not block Enroll / Unenroll / ContextUsageFor calls.
// State writes (lastUsage, threshold flags) take the lock again per-agent.
func (p *Poller) pollOnce(ctx context.Context) {
	type snapshot struct {
		name  string
		state *agentPollState
	}

	p.mu.Lock()
	snaps := make([]snapshot, 0, len(p.agents))
	for name, state := range p.agents {
		snaps = append(snaps, snapshot{name: name, state: state})
	}
	parsers := append([]Parser(nil), p.parsers...)
	onWarn := p.onWarn
	onPreFire := p.onPreFire
	onFire := p.onFire
	cfg := p.cfg
	p.mu.Unlock()

	now := p.nowFn()

	for _, snap := range snaps {
		p.pollAgent(ctx, snap.name, snap.state, parsers, cfg, now, onWarn, onPreFire, onFire)
	}
}

// pollAgent handles one agent's poll: pick a parser, parse, update state,
// fire callbacks. Mutating reads/writes on agentPollState happen under the
// Poller's lock — but parsers and callbacks run unlocked.
//
//nolint:revive // arg count is acceptable for a per-agent worker; collapsing
// these into a struct would obscure the per-cycle snapshot semantics.
func (p *Poller) pollAgent(
	ctx context.Context,
	name string,
	state *agentPollState,
	parsers []Parser,
	cfg PollerConfig,
	now time.Time,
	onWarn WarnCallback,
	onPreFire PreFireCallback,
	onFire FireCallback,
) {
	// Read enrollment + parser choice under the lock.
	p.mu.Lock()
	path := state.enrollment.TranscriptPath
	parser := state.parser
	restartInFlight := state.restartInFlight
	restartInFlightAt := state.restartInFlightAt
	p.mu.Unlock()

	// Backstop: clear stale in-flight guard so a hung restart doesn't
	// silence the agent forever. The local restartInFlight copy is not
	// re-read after this branch — downstream checks use state.restartInFlight
	// under the lock — so we don't need to update the local mirror.
	if restartInFlight && now.Sub(restartInFlightAt) >= cfg.InFlightMaxWait {
		p.mu.Lock()
		state.restartInFlight = false
		state.restartInFlightAt = time.Time{}
		p.mu.Unlock()
		slog.Warn("[contextpoll] in-flight guard cleared after timeout",
			"agent", name, "after", now.Sub(restartInFlightAt).String())
	}

	// No transcript path yet — silently skip; identity refresh will re-Enroll.
	if path == "" {
		return
	}

	// Pick a parser if we haven't already. First Matches wins, sticky-cached.
	if parser == nil {
		for _, candidate := range parsers {
			if candidate.Matches(path) {
				parser = candidate
				break
			}
		}
		if parser == nil {
			// Unknown runtime — debug-log once per cycle and skip. The agent
			// keeps polling so a parser that lands later (e.g. fixture file
			// appearing) will be picked up.
			slog.Debug("[contextpoll] no parser matched transcript",
				"agent", name, "path", path)
			return
		}
		p.mu.Lock()
		state.parser = parser
		p.mu.Unlock()
	}

	usage, err := parser.Parse(path)
	if err != nil {
		slog.Debug("[contextpoll] parse error; skipping cycle",
			"agent", name, "path", path, "err", err)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	state.lastUsage = usage

	// Post-restart reset: usage dropped back below the warn tier. Fresh
	// session, so the agent can earn new warn/pre-fire nudges from here.
	if usage.UsedPercentage < cfg.WarnThreshold {
		state.warnFired = false
		state.preFired = false
		state.preFiredAt = time.Time{}
	}

	// Inflight guard: once the force-fire has been dispatched, ignore
	// further threshold crossings until PostRestart or the InFlightMaxWait
	// backstop fires.
	if state.restartInFlight {
		return
	}

	// Force-fire: pre-fire elapsed past PreFireWait. Fire the OnFire
	// callback and set the in-flight guard.
	if state.preFired && now.Sub(state.preFiredAt) >= cfg.PreFireWait {
		if onFire != nil {
			// Release lock around callback to avoid deadlock if the
			// callback re-enters the Poller (e.g. PostRestart from inside
			// the restart trigger).
			p.mu.Unlock()
			onFire(ctx, name, usage)
			p.mu.Lock()
		}
		state.restartInFlight = true
		state.restartInFlightAt = now
		return
	}

	// Auto-tier pre-fire: emit once when usage first crosses the auto
	// threshold. PreFireWait until OnFire follows. Falls through to the
	// warn branch — a single poll that crosses both tiers should fire
	// both signals (warn discipline + pre-fire countdown).
	if usage.UsedPercentage >= cfg.AutoThreshold && !state.preFired {
		state.preFired = true
		state.preFiredAt = now
		if onPreFire != nil {
			p.mu.Unlock()
			onPreFire(ctx, name, usage)
			p.mu.Lock()
		}
	}

	// Warn tier: emit once when usage first crosses the warn threshold.
	if usage.UsedPercentage >= cfg.WarnThreshold && !state.warnFired {
		state.warnFired = true
		if onWarn != nil {
			p.mu.Unlock()
			onWarn(ctx, name, usage)
			p.mu.Lock()
		}
	}
}
