package permission

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SessionPoller polls enrolled tmux sessions, hashes pane content
// (with runtime-specific volatile-line exclusion), and invokes an
// OnStable callback when the hash is unchanged across StabilityCount
// consecutive polls. It fires at most once per stable state — when
// content changes, the poller re-arms.
//
// # Why this exists
//
// tmux's alert-silence hook is documented unreliable on detached
// sessions (tmux issue #1384). Alerts are processed per-session-
// per-client; sessions with no attached client typically do not fire
// the hook. Thrum agents run detached by design, so the daemon cannot
// trust alert-silence for the permission-prompt detection pipeline.
// This poller is the direct-control replacement.
//
// # Concurrency
//
// Safe for concurrent Enroll/Unenroll with PollOnce. PollOnce captures
// a snapshot of enrolled sessions under the poller's mutex, then
// releases it before invoking Capture and OnStable (which may block on
// I/O or other daemon paths). Per-session state updates re-acquire the
// lock. A full sync.Mutex (not RWMutex) is used — read/write mix is
// balanced at current scale and a single lock type is simpler to
// reason about.
type SessionPoller struct {
	cfg SessionPollerConfig

	mu       sync.Mutex
	enrolled map[string]*sessionState
}

// SessionPollerConfig configures a SessionPoller. All fields are
// required at construction; tests inject fakes for Capture/OnStable.
type SessionPollerConfig struct {
	// CaptureLines is the number of pane tail lines passed to Capture.
	// 30 is enough for the largest permission-prompt dialogs we've seen.
	CaptureLines int

	// StabilityCount is the number of consecutive polls with identical
	// (post-strip) hashes required before OnStable fires. 2 = one full
	// interval of no change.
	StabilityCount int

	// Capture reads the tail of a tmux pane. Default wiring uses
	// internal/tmux.CapturePane; tests inject a stub.
	Capture func(target string, lines int) (string, error)

	// OnStable is invoked once per stable state per enrolled session.
	// The callback receives the full captured content (not the stripped
	// version — the downstream detection pipeline does its own pattern
	// matching on the raw text). Errors are logged but do not affect
	// subsequent polls.
	OnStable func(ctx context.Context, session, content string) error
}

// sessionState tracks one enrolled session's debounce state.
type sessionState struct {
	runtime    string
	tmuxTarget string

	// lastHash is the sha256 of the most recent stripped-content.
	// Empty on first poll.
	lastHash [32]byte
	hasHash  bool

	// stableCount is the number of consecutive polls with the same
	// lastHash. Resets to 1 when hash changes.
	stableCount int

	// fired is true once OnStable has been invoked for the current
	// stable state. Reset to false when hash changes.
	fired bool
}

// NewSessionPoller constructs a poller from config. Does not start any
// goroutine — callers drive polling via PollOnce or Run.
func NewSessionPoller(cfg SessionPollerConfig) *SessionPoller {
	if cfg.CaptureLines <= 0 {
		cfg.CaptureLines = 30
	}
	if cfg.StabilityCount <= 0 {
		cfg.StabilityCount = 2
	}
	return &SessionPoller{
		cfg:      cfg,
		enrolled: map[string]*sessionState{},
	}
}

// Enroll registers a tmux session for polling. Idempotent — re-enrolling
// an existing session with the same (runtime, tmuxTarget) is a no-op.
// Re-enrolling with different values resets state.
func (p *SessionPoller) Enroll(session, runtime, tmuxTarget string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, ok := p.enrolled[session]
	if ok && existing.runtime == runtime && existing.tmuxTarget == tmuxTarget {
		return
	}
	p.enrolled[session] = &sessionState{
		runtime:    runtime,
		tmuxTarget: tmuxTarget,
	}
}

// Unenroll removes a session from the poller. Safe to call for sessions
// not currently enrolled.
func (p *SessionPoller) Unenroll(session string) {
	p.mu.Lock()
	delete(p.enrolled, session)
	p.mu.Unlock()
}

// EnrolledSessions returns the names of currently-enrolled sessions.
// Order is not stable. Used by tests and for daemon observability.
func (p *SessionPoller) EnrolledSessions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.enrolled))
	for name := range p.enrolled {
		out = append(out, name)
	}
	return out
}

// PollOnce runs one poll cycle across all enrolled sessions. Capture
// and OnStable run outside the lock. Errors are logged and otherwise
// ignored — one failing session does not affect others.
func (p *SessionPoller) PollOnce(ctx context.Context) {
	// Snapshot session names + fixed fields under lock so we can release
	// it before blocking I/O.
	type task struct {
		session    string
		runtime    string
		tmuxTarget string
	}
	p.mu.Lock()
	tasks := make([]task, 0, len(p.enrolled))
	for name, st := range p.enrolled {
		tasks = append(tasks, task{name, st.runtime, st.tmuxTarget})
	}
	p.mu.Unlock()

	for _, t := range tasks {
		if ctx.Err() != nil {
			return
		}
		p.pollSession(ctx, t.session, t.runtime, t.tmuxTarget)
	}
}

// pollSession runs the poll + debounce logic for one session.
func (p *SessionPoller) pollSession(ctx context.Context, session, runtime, tmuxTarget string) {
	content, err := p.cfg.Capture(tmuxTarget, p.cfg.CaptureLines)
	if err != nil {
		slog.Debug("[poller] capture failed",
			"session", session, "target", tmuxTarget, "err", err)
		return
	}

	stripped := stripVolatileLines(runtime, content)
	hash := sha256.Sum256([]byte(stripped))

	p.mu.Lock()
	st, ok := p.enrolled[session]
	if !ok {
		p.mu.Unlock()
		// Session was unenrolled between snapshot and now — drop the
		// result. Not an error.
		return
	}

	var shouldFire bool
	switch {
	case !st.hasHash:
		// First observation — establish baseline.
		st.lastHash = hash
		st.hasHash = true
		st.stableCount = 1
		st.fired = false
	case st.lastHash == hash:
		st.stableCount++
		if !st.fired && st.stableCount >= p.cfg.StabilityCount {
			shouldFire = true
			st.fired = true
		}
	default:
		// Hash changed — reset debounce.
		st.lastHash = hash
		st.stableCount = 1
		st.fired = false
	}
	p.mu.Unlock()

	if shouldFire {
		slog.Debug("[poller] stable: firing OnStable",
			"session", session, "runtime", runtime)
		if err := p.cfg.OnStable(ctx, session, content); err != nil {
			slog.Warn("[poller] OnStable callback failed",
				"session", session, "err", err)
		}
	}
}

// Run drives PollOnce on a ticker until ctx is done. Returns when ctx
// is canceled; graceful shutdown requires nothing beyond ctx.Done().
func (p *SessionPoller) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("[poller] started", "interval", interval.String())
	defer slog.Info("[poller] stopped")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.PollOnce(ctx)
		}
	}
}

// volatileLinePatterns are runtime-specific regexes matching lines
// whose content is irrelevant to pane-state detection. Stripping them
// before hashing lets the poller see "semantic silence" on runtimes
// that update cosmetic lines (spinners, elapsed timers) continuously.
//
// # Adding a new runtime
//
// Key is the runtime identifier as written to the agent identity file
// (see internal/config.IdentityFile.Runtime — canonical values include
// "claude", "codex", "opencode", "kiro-cli", "auggie", "cursor").
// Value is a slice of compiled regexes; a pane line matches ANY of the
// regexes to be stripped.
//
// Runtimes not present in this map pass through unstripped — a
// conservative default. Prefer false-not-stable (poller never fires
// for a chatty runtime with no entry) over false-stable (poller fires
// on a mid-working pane because its spinner wasn't recognized).
var volatileLinePatterns = map[string][]*regexp.Regexp{
	// codex v0.121+ displays "• Working (Ns • esc to interrupt)" while
	// an agent run is in flight. The timer updates roughly per second.
	"codex": {
		regexp.MustCompile(`^[\s•]*Working\s*\(\d+`),
	},
	// claude-code does similar with "✻ Cogitated for Ns" and similar
	// spinner lines. Patterns observed empirically; extend as new
	// spinner formats surface.
	//
	// Claude's ccstatusline adds a volatile bottom-of-pane status line
	// of the form "Model: <name> | Ctx: <size> | Block: <timer> | Ctx:
	// <pct>" whose Ctx size and Block countdown drift every 30-60s.
	// Without stripping, the cadence hash destabilizes for unchanged
	// prompts and fires duplicate nudges (observed 3x in ~80s during
	// thrum-48kt.2 E2E setup — tracked as thrum-ptcj). The regex
	// requires BOTH "Ctx:" AND "Block:" markers in order so that user
	// text containing either token in isolation passes through.
	"claude": {
		regexp.MustCompile(`^[\s*✻✽✾✿✢]*(?:Cogitat|Workin|Think|Plann|Analyz)`),
		regexp.MustCompile(`\(\d+s\s*·\s*(?:esc|⚒)`),
		regexp.MustCompile(`\bCtx:\s\S.*\bBlock:\s\S`),
	},
}

// stripVolatileLines returns content with lines matching the runtime's
// volatile-line patterns removed. Unknown runtimes pass through
// unchanged (conservative — better to have false-not-stable than
// false-stable).
func stripVolatileLines(runtime, content string) string {
	patterns, ok := volatileLinePatterns[runtime]
	if !ok || len(patterns) == 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	kept := lines[:0] // reuse backing array
	for _, line := range lines {
		skip := false
		for _, re := range patterns {
			if re.MatchString(line) {
				skip = true
				break
			}
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
