package agentdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/runtime/claude"
	"github.com/leonletto/thrum/internal/skills/mirror"
	"github.com/leonletto/thrum/internal/worktree"
)

// ErrTargetSessionAlive is returned by stage 0 when CheckPane reports
// a live tmux session for the target agent. Refusing the wake here
// protects an in-flight agent from being clobbered by a clashing
// scheduled-agent dispatch. Callers MUST check via errors.Is rather
// than string-matching — wrapped variants surface from helper layers.
var ErrTargetSessionAlive = errors.New("agentdispatch: target tmux session already alive")

// TmuxRPC is the minimal tmux-side surface ScheduledAgentHandler needs
// from the daemon's RPC layer. Declared here (rather than imported from
// internal/daemon/rpc) so agentdispatch stays free of import cycles —
// the adapter at cmd/thrum/main.go wires rpc.TmuxHandler to this
// interface at daemon boot. Tests mock TmuxRPC directly.
type TmuxRPC interface {
	// CheckPane reports whether `target` already owns a live tmux pane.
	// Used by stage 0 to refuse a wake that would clobber an existing
	// agent. Returns (false, nil) for "no live session" — distinct
	// from (false, err) for "could not determine".
	CheckPane(ctx context.Context, target string) (bool, error)

	// TmuxCreate provisions a detached tmux session named `opts.SessionName`
	// rooted at `opts.Cwd`. Stage 4.
	TmuxCreate(ctx context.Context, target string, opts TmuxCreateOpts) error

	// TmuxLaunch invokes the runtime command (claude, codex, ...) inside
	// the session. Stage 5.
	TmuxLaunch(ctx context.Context, target string) error

	// WaitForPaneReady blocks until the runtime's pane is responsive
	// (prompt rendered, stdin accepting input). Stage 6.
	WaitForPaneReady(ctx context.Context, target string) error

	// TmuxKillSession terminates the session. Used by rollback paths
	// (stages 5/6 failures) and by graceful teardown in stage 8.
	TmuxKillSession(ctx context.Context, target string) error

	// PaneSendCtrlCExit sends the SIGTERM-equivalent keystroke sequence
	// (Ctrl-C followed by `exit\n`) to give the runtime a graceful exit
	// chance during stage 8 teardown. Followed by a configurable grace
	// window before TmuxKillSession fires.
	PaneSendCtrlCExit(ctx context.Context, target string) error

	// PaneInjectPrompt sends a text prompt to the runtime's pane via
	// send-keys + 200ms gap + Enter (matching the canonical
	// sendKeysAndSubmit helper in internal/daemon/rpc/tmux.go so we
	// defeat paste-mode detection per feedback_byte_equality_pane_detection
	// memory). Used by E6.4's multi-fire idle-nudge loop to inject
	// the operator-visible nudge prompt each fire.
	PaneInjectPrompt(ctx context.Context, target, text string) error
}

// TmuxCreateOpts carries the per-session knobs TmuxCreate needs.
type TmuxCreateOpts struct {
	// Cwd is the working directory the session opens in — typically
	// the freshly-created worktree path from stage 3a.
	Cwd string

	// SessionName is the tmux session identifier; canonical convention
	// is the agent's target name (e.g. "docs_bot").
	SessionName string
}

// MessageRPC is the minimal message-send surface stage 2 needs. Stays
// here rather than importing internal/daemon/rpc to keep agentdispatch
// cycle-free; cmd/thrum/main.go's adapter wires rpc.MessageHandler.
type MessageRPC interface {
	// MessageSend enqueues an inbox message for `target` with the given
	// subject + body, returning the persisted message id. The id is
	// the wake_message_id that stage 2 journals atomically.
	MessageSend(ctx context.Context, target, subject, body string) (string, error)
}

// WorktreeManager is the minimal worktree-side surface stages 3a, 4-6
// rollback, and stage 8 teardown reach for. The package-level
// internal/worktree functions get adapted to this interface in
// cmd/thrum/main.go so tests can swap fakes without touching real git
// state. Stage 3a calls Create; rollback + stage 8 call Destroy.
//
// The error sentinels (ErrPathExists, ErrPersistentBranchMismatch,
// context.Canceled, context.DeadlineExceeded) flow through unchanged —
// classifyWorktreeError keys off them via errors.Is.
type WorktreeManager interface {
	Create(ctx context.Context, opts worktree.CreateOpts) (*worktree.CreateResult, error)
	Destroy(ctx context.Context, opts worktree.DestroyOpts) (*worktree.DestroyResult, error)
}

// MirrorWorker is the minimal skill-mirror surface stage 3b consumes.
// Currently a single method per C-B1 E9.5; new methods land here only
// when a B-B1 stage needs them. mirror.ErrNullAdapter is the canonical
// success-as-error sentinel (some runtimes have no mirror path).
type MirrorWorker interface {
	EnsureMirrored(ctx context.Context, worktreePath string) error
}

// EscalationRouter is the minimal escalation surface stages 4-7 reach
// for when they need to page an operator. Task 20 ships the real
// implementation in internal/daemon/escalation; this interface keeps
// scheduled_agent.go decoupled from the routing details.
//
// The Alert parameter is the canonical escalation.Alert struct from
// internal/daemon/escalation — sharing it avoids a silent-divergence
// risk between two structurally-identical Alert definitions.
type EscalationRouter interface {
	Route(ctx context.Context, alert escalation.Alert, subject, body string) error
}

// RPCDrainer drains in-flight agent-side RPCs (agent.listFiles +
// agent.getFile per spec §7.1 stage 8 + E6.6 contract) during
// teardown so cleanup doesn't race a writing runtime. E6.6's
// implementer ships the real RPC tracker; E6.1 ships the interface
// + a nil-safe call site so the canonical teardown ordering
// (PaneSendCtrlCExit → waitForPaneExit → Drain → TmuxKillSession →
// worktree.Destroy) is established before E6.6 lands.
type RPCDrainer interface {
	DrainListFiles(ctx context.Context, target string, grace time.Duration) error
}

// Reconciler is the boot-time recovery surface ScheduledAgentHandler
// delegates to per spec §7.7. The real implementation ships in E6.9
// (sub-epic thrum-6qmf.4.65): enumerate non-terminal runs at boot,
// classify each (resumable worktree intact, terminal-failed, lost-
// track), and return the resolved state so scheduler advances
// scheduler_job_state correctly.
//
// E6.1 ships the interface declaration so the Handler.Reconcile
// satisfaction compiles cleanly across the integration boundary;
// E6.9 supplies a real implementation that gets injected via Deps.
type Reconciler interface {
	ReconcileRun(ctx context.Context, job scheduler.JobSpec, runID string, lastState scheduler.State) (scheduler.State, error)
}

// (EscalationAlert was previously a local struct here; it has been
// consolidated into escalation.Alert so the router contract is
// expressed by exactly one type. See EscalationRouter above.)

// Deps carries every external dependency ScheduledAgentHandler needs
// from cmd/thrum/main.go's wiring layer. Every field is an interface
// so tests can swap real implementations for mocks without touching
// the handler code.
//
// Per IMPORTANT #7 from plan v1 dual-review: ScheduledAgentHandler is
// shared across concurrent dispatches (AC 9.2.10 race-detector clean
// for 5 simultaneous dispatches). All per-run state lives in
// stack/parameter scope, never on the handler struct.
type Deps struct {
	// RepoPath is the absolute path to the daemon-managed repository.
	// Used by worktree.Create and worktree.Destroy callers.
	RepoPath string

	// Tmux + Message wrap the daemon's existing RPC machinery; the
	// adapter in cmd/thrum/main.go wires rpc.TmuxHandler / rpc.MessageHandler.
	Tmux    TmuxRPC
	Message MessageRPC

	// Worktree wraps internal/worktree.Create + Destroy. Stage 3a
	// consumes Create; stage 4-6 rollback + stage 8 teardown consume
	// Destroy. The cmd/thrum/main.go adapter forwards directly to the
	// package functions; tests swap a fake to exercise error paths
	// (ErrPathExists / ErrPersistentBranchMismatch / context.Canceled).
	Worktree WorktreeManager

	// Registry is the agents-table read/write surface (E6.0 Task 4.5).
	// Stage 7 idle-nudge fire path + stage 8 teardown read agent state
	// through it; loop-guard + state-md ack flows mutate via setters.
	Registry agent.AgentRegistry

	// Mirror is C-B1 E9.5's EnsureMirrored worker. Stage 3b calls
	// EnsureMirrored against the worktree path returned by stage 3a.
	// ErrNullAdapter is treated as success per C-B1 §12.3.1.
	Mirror MirrorWorker

	// Escalation routes alerts to operator via the right channel
	// (email when configured, supervisor agent otherwise). Task 20
	// supplies the real implementation.
	Escalation EscalationRouter

	// Reconciler ships the boot-time recovery logic per spec §7.7.
	// E6.9 (thrum-6qmf.4.65) provides the real implementation; E6.1
	// allows nil so Dispatch-only fixtures can construct a handler
	// without wiring it. Reconcile() guards against nil at the call
	// site rather than panicking on uninjected Deps.
	Reconciler Reconciler

	// Drainer waits for in-flight agent-side RPCs to settle during
	// stage-8 teardown (per spec §7.1 + AC 9.2.9). E6.6 ships the
	// real implementation; E6.1 allows nil so the teardown skips
	// the drain step when the dep isn't wired (preserves the
	// canonical ordering when partial deps are injected).
	Drainer RPCDrainer
}

// ScheduledAgentHandler implements scheduler.Handler for the
// "scheduled_agent" job type per spec §7.1. The 9-stage protocol
// (stages 0-8 + dynamic idle_nudge_NofM during stage 7) is driven
// by Dispatch; Reconcile (stub here, owned by E6.9) handles boot-
// time recovery for non-terminal runs.
//
// All per-run state (jobspec, runID, reporter, worktree path,
// completion signal channel) flows through method parameters or
// closure capture in stage-helper methods. No mutable run state
// lives on the handler struct so concurrent Dispatch invocations
// don't share writeable fields.
type ScheduledAgentHandler struct {
	deps Deps
}

// NewScheduledAgentHandler returns a handler ready to register with
// the A-B1 scheduler via scheduler.RegisterTypeHandler("scheduled_agent", h).
// Caller owns the Deps lifecycle — the handler stores them as-is.
func NewScheduledAgentHandler(deps Deps) *ScheduledAgentHandler {
	return &ScheduledAgentHandler{deps: deps}
}

// Stages declares the canonical nine-stage dwell budget per spec §7.1.
// Per-stage durations are upper bounds the A-B4 stalled-sweep consults
// to decide when an in-stage agent is wedged.
//
// StageRunningWork is intentionally generous (24h) — the multi-fire
// idle-nudge loop bounds the in-stage dwell, not this Stages() entry.
// Without the wide ceiling, AC-9.2.10 long-running tests would trip
// the sweep prematurely.
func (h *ScheduledAgentHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{
		StageNameCollisionCheck:  5 * time.Second,
		StageBudgetCheck:         5 * time.Second,
		StageEnqueueWakeMessage:  10 * time.Second,
		StageCreatingWorktree:    60 * time.Second, // includes EnsureMirrored sub-action
		StageCreatingTmuxSession: 30 * time.Second,
		StageLaunchingRuntime:    30 * time.Second,
		StageWaitingForPaneReady: 60 * time.Second,
		StageRunningWork:         24 * time.Hour,
		StageTearingDown:         30 * time.Second,
	}
}

// Dispatch implements scheduler.Handler.Dispatch for scheduled_agent
// jobs. Per IMPORTANT #7 dual-review: all per-run state (jobspec,
// runID, reporter, signals) lives in parameter/stack scope — no
// mutable fields on the receiver.
//
// E6.1 Task 10 implements stage 0 (name-collision check); subsequent
// tasks fill in stages 1-8.
func (h *ScheduledAgentHandler) Dispatch(ctx context.Context, job scheduler.JobSpec, runID string, reporter scheduler.StateReporter, signals <-chan *scheduler.Completion) error {
	target := ""
	if job.ScheduledAgent != nil {
		target = job.ScheduledAgent.Target
	}

	// Stage 0: name-collision check. Refuses the wake if a tmux pane
	// already exists for `target` so we never clobber a live agent.
	// The CheckPane-error path is distinct from the alive-true path:
	// the former is "could not determine", the latter is the
	// canonical "another session is up" rejection.
	if err := reporter.Stage(StageNameCollisionCheck); err != nil {
		return err
	}
	paneAlive, err := h.deps.Tmux.CheckPane(ctx, target)
	if err != nil {
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("stage 0: CheckPane error: %v", err), nil)
		return fmt.Errorf("stage 0: CheckPane(%q): %w", target, err)
	}
	if paneAlive {
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("stage 0: target agent %s already has a live tmux session", target),
			map[string]any{"target": target})
		return ErrTargetSessionAlive
	}

	// Stage 1: budget_check — observability marker only. A-B1's reactor
	// performs the actual budget check BEFORE invoking Dispatch (per
	// spec §7.1 + canonical Q-Spec-3 resolution / Leon's 2026-05-15
	// answer); over-budget jobs never reach this handler. Emitting the
	// marker keeps the nine-stage walk visible in scheduler_job_events
	// so `thrum cron history` + A-B4 stalled-sweep see the full
	// dispatched → ... → completed sequence (MINOR #6 from plan v1
	// dual-review prompted this reframing).
	if err := reporter.Stage(StageBudgetCheck); err != nil {
		return err
	}

	// Stage 2: enqueue agent.wake message. The wire format (JSON-in-
	// fenced-block per spec §7.4) is composed by buildWakeMessage; the
	// returned message ID is journaled atomically on the StateRunning
	// transition so A-B4's stalled-sweep + post-crash recovery have an
	// audit pointer back to the inbox row that primed this wake.
	if err := reporter.Stage(StageEnqueueWakeMessage); err != nil {
		return err
	}
	wakeBody := buildWakeMessage(job, runID)
	subject := fmt.Sprintf("Wake: %s @ %s", job.ID, time.Now().Format(time.RFC3339))
	messageID, err := h.deps.Message.MessageSend(ctx, target, subject, wakeBody)
	if err != nil {
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("stage 2: agent.wake enqueue failed: %v", err), nil)
		return fmt.Errorf("stage 2: agent.wake enqueue failed: %w", err)
	}
	if err := reporter.Transition(scheduler.StateRunning, "stage 2 complete", map[string]any{
		"wake_message_id": messageID,
	}); err != nil {
		return err
	}

	// Stage 3a: worktree.Create. Failure-contract per thrum-non7 §3.5
	// is "zero residue on non-cancel errors" — no inline rollback is
	// needed for ErrPathExists or ErrPersistentBranchMismatch. The
	// context-cancellation case (cancel arriving AFTER git worktree add
	// succeeds) is the one residue-class the thrum-non7 contract
	// explicitly defers to E6.9 sweep (spec §7.1 stage 3 + thrum-non7
	// §3.7) — so the cancel path emits NO worktree_path journal entry.
	if err := reporter.Stage(StageCreatingWorktree); err != nil {
		return err
	}
	persistent := false
	baseBranch := ""
	if job.ScheduledAgent != nil {
		persistent = job.ScheduledAgent.WorktreePersistent
		baseBranch = job.ScheduledAgent.BaseBranch
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	wakeTimestamp := time.Now().Unix()
	createOpts := worktree.CreateOpts{
		RepoPath:      h.deps.RepoPath,
		BasePath:      "", // fall through to worktree.InferBasePath
		AgentName:     target,
		BaseBranch:    baseBranch,
		Persistent:    persistent,
		WakeTimestamp: wakeTimestamp,
	}
	// JobID is ULID-clean only (alphanumeric + underscore per
	// worktree.validateOpts); JobSpec.IDs use hyphenated kebab-case.
	// Sanitize by replacing hyphens with underscores so the leaf path
	// remains scoped to the originating job spec without leaking out
	// of the worktree validator's alphabet. Persistent mode skips
	// JobID/WakeTimestamp validation altogether.
	if !persistent {
		createOpts.JobID = strings.ReplaceAll(job.ID, "-", "_")
	}
	createResult, err := h.deps.Worktree.Create(ctx, createOpts)
	if err != nil {
		return h.classifyWorktreeError(err, reporter)
	}

	// Stage 3b: skillmirror.EnsureMirrored. Has its own rollback
	// semantics because stage 3a already returned successfully — a
	// non-cancel mirror failure leaves a fully-created worktree
	// behind, so the handler must inline-destroy it (with the
	// destroyed-paths recorded in the failure event details for the
	// audit trail).
	//
	// IMPORTANT #8 fix from plan v1 dual-review: the three-arm switch
	// (success/null-adapter, context-cancel, hard-fail-with-rollback)
	// lives in handleStage3bMirror as a helper returning
	// (treatAsSuccess bool, err error) instead of inline goto/label.
	// The straight-line caller cannot accidentally pick up a future
	// compile-trap if a `var` declaration ever lands between the goto
	// site and the label.
	if _, mirrorErr := h.handleStage3bMirror(ctx, createResult, reporter); mirrorErr != nil {
		return mirrorErr
	}

	// Atomic journal-write closing stage 3 (after BOTH sub-actions
	// succeed OR EnsureMirrored returned ErrNullAdapter, which
	// canonical §3.5 + C-B1 §12.3.1 treat as success).
	if err := reporter.Transition(scheduler.StateRunning, "stage 3 complete", map[string]any{
		"worktree_path": createResult.Path,
		"branch_name":   createResult.Branch,
		"reused":        createResult.Reused,
	}); err != nil {
		return err
	}

	// Stage 4: tmux create. Failures here trigger
	// rollbackStage4Failure (worktree.Destroy), since stage 3 left a
	// fully-created worktree behind that won't be reaped by the
	// stage-3a zero-residue contract.
	if err := reporter.Stage(StageCreatingTmuxSession); err != nil {
		return err
	}
	if err := h.deps.Tmux.TmuxCreate(ctx, target, TmuxCreateOpts{
		Cwd:         createResult.Path,
		SessionName: target,
	}); err != nil {
		h.rollbackStage4Failure(createResult)
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("stage 4: tmux create: %v", err),
			map[string]any{
				"worktree_path_destroyed": createResult.Path,
				"branch_name_destroyed":   createResult.Branch,
			})
		return fmt.Errorf("stage 4: tmux create: %w", err)
	}

	// Atomic journal-write recording the tmux session + the per-wake
	// transcript directory (canonical §8.2). The transcript_dir is
	// path-derived from the worktree + wake timestamp; the agentName
	// argument is reserved for a future Claude Code hash change and
	// has no effect today.
	transcriptDir := claude.TranscriptDir(createResult.Path, target, wakeTimestamp)
	if err := reporter.Transition(scheduler.StateRunning, "stage 4 complete", map[string]any{
		"tmux_session_name": target,
		"transcript_dir":    transcriptDir,
	}); err != nil {
		return err
	}

	// Stage 5: tmux launch. Spawns the runtime command (claude / codex
	// / etc.) inside the tmux session created in stage 4. Failures
	// here trigger rollbackStage5Failure — kill-session + destroy
	// worktree — since both artifacts are live.
	if err := reporter.Stage(StageLaunchingRuntime); err != nil {
		return err
	}
	if err := h.deps.Tmux.TmuxLaunch(ctx, target); err != nil {
		h.rollbackStage5Failure(target, createResult)
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("stage 5: tmux launch: %v", err),
			map[string]any{
				"worktree_path_destroyed": createResult.Path,
				"branch_name_destroyed":   createResult.Branch,
				"tmux_session_killed":     target,
			})
		return fmt.Errorf("stage 5: tmux launch: %w", err)
	}

	// Stage 6: wait for the runtime's pane to render its prompt and
	// accept input. Same rollback row as stage 5 — kill-session +
	// destroy worktree. Failure reason carries "pane-ready timeout"
	// (the canonical operator-facing string), since timeouts dominate
	// this failure class.
	if err := reporter.Stage(StageWaitingForPaneReady); err != nil {
		return err
	}
	if err := h.deps.Tmux.WaitForPaneReady(ctx, target); err != nil {
		h.rollbackStage5Failure(target, createResult)
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("stage 6: pane-ready timeout: %v", err),
			map[string]any{
				"worktree_path_destroyed": createResult.Path,
				"branch_name_destroyed":   createResult.Branch,
				"tmux_session_killed":     target,
			})
		return fmt.Errorf("stage 6: pane-ready timeout: %w", err)
	}

	// Stage 7: running_work + idle-nudge select loop. The loop arms
	// a timer on the operator-configured idle window and waits for
	// one of three things to happen:
	//   - ctx.Done(): operator cancel → teardown + StateCancelled.
	//   - signals: job.done RPC delivered a Completion → teardown +
	//     StateCompleted with the summary recorded in journal details.
	//   - timer.C: idle window expired → loop.onTimerFire runs one
	//     tick of the multi-fire protocol (E6.4 in idle_nudge.go) —
	//     PaneActivity probe, settle/re-arm if active, idle_nudge_NofM
	//     stage marker + nudge inject if silent, Layer-D escalation
	//     when nudgesFired hits maxNudges.
	//
	// Per IMPORTANT #7 dual-review: idleNudgeLoop is per-call (stack
	// scope), never a handler field. AC 9.2.10 (5 simultaneous
	// dispatches, race-detector clean) depends on this.
	if err := reporter.Stage(StageRunningWork); err != nil {
		return err
	}
	// Canonical defaults per spec §7.1: idle=90s, maxNudges=5,
	// grace=10s. Overridden per-job when ScheduledAgent is populated.
	idleSeconds, maxNudges, graceSeconds := 90, 5, 10
	if job.ScheduledAgent != nil {
		idleSeconds = defaultIfZero(job.ScheduledAgent.IdleNudgeSeconds, 90)
		maxNudges = defaultIfZero(job.ScheduledAgent.MaxIdleNudges, 5)
		graceSeconds = defaultIfZero(job.ScheduledAgent.TeardownGraceSeconds, 10)
	}
	loop := &idleNudgeLoop{
		target:           target,
		runID:            runID,
		idleSeconds:      idleSeconds,
		maxNudges:        maxNudges,
		lastPaneActivity: time.Now(),
		timer:            time.NewTimer(time.Duration(idleSeconds) * time.Second),
		tmux:             h.deps.Tmux,
		escalation:       h.deps.Escalation,
	}
	defer loop.timer.Stop()

	for {
		select {
		case <-ctx.Done():
			h.teardownGracefully(target, createResult, graceSeconds, job, reporter)
			_ = reporter.Transition(scheduler.StateCancelled, "operator cancelled", nil)
			return ctx.Err()
		case completion := <-signals:
			h.teardownGracefully(target, createResult, graceSeconds, job, reporter)
			details := map[string]any{}
			if completion != nil {
				details["summary"] = completion.Summary
			}
			_ = reporter.Transition(scheduler.StateCompleted, "agent reported done", details)
			return nil
		case <-loop.timer.C:
			// E6.4 multi-fire body: PaneActivity probe → if active,
			// reset with 2s settle; if silent, increment counter +
			// emit idle_nudge_NofM stage marker; at maxNudges fire
			// Layer-D escalation (StateFailed + escalation_emitted_by
			// marker for A-B1's evaluator-side suppression) and
			// return ErrIdleNudgeExhausted to close the dispatch.
			if err := loop.onTimerFire(ctx, reporter); err != nil {
				return err
			}
		}
	}
}

// defaultIfZero returns fallback when v is zero, else v. Used to
// merge JobSpec.ScheduledAgent.* operator-configurable knobs with
// canonical defaults per spec §7.1 (idleSeconds=90, maxNudges=5,
// graceSeconds=10).
func defaultIfZero(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

// idleNudgeLoop + onTimerFire moved to idle_nudge.go in E6.4 Task 36.
// The type declaration + canonical multi-fire body live there alongside
// the supporting helpers (idleNudgePrompt, Layer-D body builder).

// teardownGracefully runs the canonical stage-8 teardown sequence
// per spec §7.1 stage 8 — five steps in fixed order:
//
//  1. PaneSendCtrlCExit (SIGTERM-equivalent: Ctrl-C + `exit\n`) so
//     the runtime gets a graceful-exit window before kill-session.
//  2. waitForPaneExit(graceSeconds) — block up to grace seconds for
//     the runtime to actually exit. Spares cleanup from racing the
//     runtime's own shutdown writes.
//  3. drainListFilesRPCs(graceSeconds) — wait for in-flight
//     agent.listFiles / agent.getFile RPCs to settle (E6.6 ships
//     the real helper; E6.1 stubs as a no-op so the canonical
//     ordering is documented at the call site).
//  4. TmuxKillSession — hard-kill if step 2 timed out.
//  5. worktree.Destroy — ephemeral mode deletes branch alongside
//     the path; persistent mode passes Branch="" so the branch is
//     preserved for the next scheduled wake to reuse.
//
// Per IMPORTANT #7 dual-review: `job` and `reporter` are PARAMETERS,
// not handler fields, so concurrent Dispatches don't race on
// per-run state. AC 9.2.10 (5 simultaneous dispatches, race clean)
// pins this.
// (signature note) ctx is intentionally NOT a parameter: every
// child call inside teardownGracefully uses context.Background() so
// cleanup completes even when the parent context is already
// cancelled. Threading a context through would invite a future
// implementer to propagate it — which would defeat the cancel-path
// cleanup invariant. Per-call context is constructed internally
// (waitForPaneExit, drainListFilesRPCs both use their own).
func (h *ScheduledAgentHandler) teardownGracefully(target string, result *worktree.CreateResult, graceSeconds int, job scheduler.JobSpec, reporter scheduler.StateReporter) {
	_ = reporter.Stage(StageTearingDown)

	grace := time.Duration(graceSeconds) * time.Second

	// SIGTERM-equivalent first — give the runtime its grace window
	// to flush state.md, in-flight messages, etc.
	_ = h.deps.Tmux.PaneSendCtrlCExit(context.Background(), target)

	h.waitForPaneExit(target, grace)

	// E6.6's listFiles drain is owned by the file-streaming epic;
	// stub here so the canonical ordering is fixed before E6.6
	// drops in the real wait.
	h.drainListFilesRPCs(target, grace)

	// Hard-kill if the runtime didn't exit on its own during the
	// grace window. TmuxKillSession is idempotent (no-op on already-
	// dead sessions) so calling it after a successful graceful exit
	// is safe.
	_ = h.deps.Tmux.TmuxKillSession(context.Background(), target)

	// Persistent mode: skip branch deletion (spec §7.1 + Q-Spec
	// preservation contract). Ephemeral mode: delete branch alongside
	// the worktree path so a re-run isn't blocked by stale state.
	branch := result.Branch
	if job.ScheduledAgent != nil && job.ScheduledAgent.WorktreePersistent {
		branch = ""
	}
	_, _ = h.deps.Worktree.Destroy(context.Background(), worktree.DestroyOpts{
		RepoPath:     h.deps.RepoPath,
		WorktreePath: result.Path,
		Branch:       branch,
		Force:        true,
	})
}

// waitForPaneExit polls TmuxRPC.CheckPane until the pane reports
// not-alive OR the grace window expires. Polling cadence (100ms) is
// fast enough that a runtime exiting in well under graceSeconds gets
// detected almost immediately, but slow enough that the daemon
// doesn't spam CheckPane during a stuck shutdown.
//
// Uses context.Background() with a timeout so the wait completes
// even if the parent context is cancelled — see teardownGracefully's
// invariant that cleanup runs on cancel paths too.
func (h *ScheduledAgentHandler) waitForPaneExit(target string, grace time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			alive, err := h.deps.Tmux.CheckPane(ctx, target)
			if err != nil || !alive {
				return
			}
		}
	}
}

// drainListFilesRPCs delegates to the injected RPCDrainer per spec
// §7.1 stage 8 + AC 9.2.9. When Drainer is nil (E6.6 not wired,
// fixture-only handler), the drain is a no-op so the canonical
// teardown ordering is preserved — operator visibility into the
// missing drain comes from the daemon-boot config check, not from a
// runtime nil-deref at teardown time. Drainer errors are absorbed
// (best-effort cleanup); the kill-session path that follows assumes
// the drain may have timed out.
func (h *ScheduledAgentHandler) drainListFilesRPCs(target string, grace time.Duration) {
	if h.deps.Drainer == nil {
		return
	}
	_ = h.deps.Drainer.DrainListFiles(context.Background(), target, grace)
}

// routeEscalation is the canonical call site for all five spec §8
// escalation sources: idle-nudge exhaustion (E6.4), stage-failure
// 3-consecutive (E6.1), auto-respawn loop guard (E6.7), state.md
// parse failure (E6.2), nudge target offline (E6.3). Future
// implementers add Route() invocations against this helper rather
// than reaching for h.deps.Escalation directly so the nil guard
// applies uniformly across the substrate — a partial-config
// deployment (Escalation interface unwired) returns nil rather
// than nil-deref'ing on the first escalation event.
//
// Errors from the underlying router are returned as-is so callers
// can decide whether to log + continue (most cases) or surface
// (e.g., the auto-respawn loop guard might choose to fail the
// dispatch if the operator can't be reached).
//
// E6.1 ships this helper + the nil guard pattern; no call sites
// fire yet (the consecutive-failure counter is A-B1's responsibility
// and the read+route plumbing lands with the next E6.x sub-epic
// per the spec §8 attribution table).
func (h *ScheduledAgentHandler) routeEscalation(ctx context.Context, alert escalation.Alert, subject, body string) error {
	if h.deps.Escalation == nil {
		return nil
	}
	return h.deps.Escalation.Route(ctx, alert, subject, body)
}

// handleStage3bMirror runs the skill-mirror sub-action and classifies
// the result per spec §7.1 stage 3b + C-B1 §12.3.1. Returns:
//
//   - (true, nil) on EnsureMirrored success OR ErrNullAdapter (the
//     latter is success-skip — some runtimes have no mirror surface
//     in v0.11; the agent reads skills directly from the worktree).
//   - (false, err) on context.Canceled / context.DeadlineExceeded
//     with a StateCancelled transition recorded and NO inline
//     worktree.Destroy (per thrum-non7 §3.7 the E6.9 sweep reclaims
//     the orphan; inline rollback would race the sweep).
//   - (false, err) on any other error with an inline worktree.Destroy
//     (best-effort, using context.Background() so the cleanup runs
//     even when the parent context is cancelled), the destroyed paths
//     recorded in the failure event details, and a StateFailed
//     transition.
//
// The (bool, error) shape encodes IMPORTANT #8 from plan v1 dual-
// review: keeping the success/null-adapter distinction available to
// future telemetry without forcing a goto in the caller.
func (h *ScheduledAgentHandler) handleStage3bMirror(ctx context.Context, result *worktree.CreateResult, reporter scheduler.StateReporter) (bool, error) {
	err := h.deps.Mirror.EnsureMirrored(ctx, result.Path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, mirror.ErrNullAdapter) {
		slog.Debug("stage 3b: ErrNullAdapter — runtime has no mirror path",
			"worktree_path", result.Path)
		return true, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		_ = reporter.Transition(scheduler.StateCancelled,
			"stage 3b cancelled mid-mirror", nil)
		return false, err
	}
	// Non-cancel hard failure: inline rollback. context.Background()
	// so the cleanup completes even if the parent context is already
	// cancelled (cleanup must not be skipped just because the daemon
	// is shutting down on a different code path).
	_, _ = h.deps.Worktree.Destroy(context.Background(), worktree.DestroyOpts{
		RepoPath:     h.deps.RepoPath,
		WorktreePath: result.Path,
		Branch:       result.Branch,
		Force:        true,
	})
	_ = reporter.Transition(scheduler.StateFailed,
		fmt.Sprintf("stage 3b: skill mirror failed: %v", err),
		map[string]any{
			"worktree_path_destroyed": result.Path,
			"branch_name_destroyed":   result.Branch,
		})
	return false, fmt.Errorf("stage 3b: skill mirror failed: %w", err)
}

// classifyWorktreeError maps stage 3a errors to the canonical
// StateFailed reasons + Cancelled state per spec §7.1 stage 3 and
// thrum-non7 §3.5/§3.7. Four cases:
//
//   - context.Canceled / DeadlineExceeded → StateCancelled with NO
//     worktree_path journal-write; the E6.9 sweep is responsible for
//     reclaiming the in-flight residue.
//   - ErrPathExists → StateFailed "queued for next-boot sweep" (stale
//     worktree at expected path; sweep reaps).
//   - ErrPersistentBranchMismatch → StateFailed "manual reconciliation
//     required" (operator-owned branch squatted the agent path).
//   - default → StateFailed with the raw error string.
//
// The error is returned wrapped (via fmt.Errorf %w on non-sentinel
// paths) so callers can still errors.Is(...) against the underlying
// sentinels — important for AC 9.2 acceptance pinning.
func (h *ScheduledAgentHandler) classifyWorktreeError(err error, reporter scheduler.StateReporter) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		_ = reporter.Transition(scheduler.StateCancelled, "stage 3a cancelled mid-create", nil)
		return fmt.Errorf("stage 3a: %w", err)
	case errors.Is(err, worktree.ErrPathExists):
		_ = reporter.Transition(scheduler.StateFailed,
			"stage 3a: stale worktree at expected path; queued for next-boot sweep",
			map[string]any{"original_err": err.Error()})
		return fmt.Errorf("stage 3a: %w", err)
	case errors.Is(err, worktree.ErrPersistentBranchMismatch):
		_ = reporter.Transition(scheduler.StateFailed,
			"stage 3a: persistent worktree squatted by operator-owned branch; manual reconciliation required",
			map[string]any{"original_err": err.Error()})
		return fmt.Errorf("stage 3a: %w", err)
	default:
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("stage 3a: worktree.Create: %v", err), nil)
		return fmt.Errorf("stage 3a: worktree.Create: %w", err)
	}
}

// buildWakeMessage composes the agent.wake message body per spec §7.4:
// JSON inside a markdown fenced block so the message is both human-
// readable in the inbox and machine-parseable by the agent-side lean-
// prime skill (E6.2).
//
// prior_run_summary is nullable; first-wake produces null. Sourcing the
// non-nil case from scheduler_job_state.last_completion_summary is
// deferred — the state-store field doesn't exist yet, and the spec
// §7.4 wire format already permits the null encoding for the unavailable
// case. When the state-store column lands, plumb it here without
// changing the wire shape.
func buildWakeMessage(job scheduler.JobSpec, runID string) string {
	var primer string
	if job.ScheduledAgent != nil {
		primer = job.ScheduledAgent.Primer
	}
	body := map[string]any{
		"kind":              "agent.wake",
		"job_id":            job.ID,
		"run_id":            runID,
		"scheduled_at":      time.Now().Format(time.RFC3339),
		"wake_reason":       "scheduled",
		"primer":            primer,
		"prior_run_summary": nil,
	}
	// json.MarshalIndent on a map[string]any with only JSON-safe values
	// cannot fail; error path is unreachable in practice. Ignore err
	// rather than panic so a hypothetical regression in upstream Go
	// (or a future field-type change) doesn't crash the dispatcher.
	jsonBlob, _ := json.MarshalIndent(body, "", "  ")
	return fmt.Sprintf("```json\n%s\n```\n", string(jsonBlob))
}

// Reconcile implements scheduler.Handler.Reconcile for boot-time
// recovery per spec §7.7. Delegates to the injected Reconciler;
// when no Reconciler is wired (E6.9 hasn't landed yet, or a fixture-
// only test handler), returns lastState unchanged so the substrate
// continues to treat the run as it was. Returning ErrLostTrack here
// would punish runs against a partial-config daemon, which is the
// wrong default — let E6.9's real body make the classification.
func (h *ScheduledAgentHandler) Reconcile(ctx context.Context, job scheduler.JobSpec, runID string, lastState scheduler.State) (scheduler.State, error) {
	if h.deps.Reconciler == nil {
		return lastState, nil
	}
	return h.deps.Reconciler.ReconcileRun(ctx, job, runID, lastState)
}
