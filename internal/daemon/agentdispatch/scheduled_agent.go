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
type EscalationRouter interface {
	Route(ctx context.Context, alert EscalationAlert, subject, body string) error
}

// EscalationAlert tags the source of an escalation so the router can
// pick the right delivery channel.
type EscalationAlert struct {
	Source    string // canonical sources: "b-b1.idle_nudge", "b-b1.stage_failure", "b-b1.auto_respawn_loop_guard"
	AgentName string
	JobID     string
	RunID     string
}

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

	// TODO(thrum-6qmf.4.56..thrum-6qmf.4.61): implement stages 5-8.
	return nil
}

// rollbackStage4Failure tears down the stage-3 worktree (path + branch)
// after a stage-4 tmux-create failure. context.Background() so the
// cleanup completes even when the parent context is already cancelled
// — daemon shutdown shouldn't strand a worktree just because the
// failing stage's context happened to be the one that got cancelled.
//
// Task 17 extracts this (and rollbackStage5Failure for stages 5/6)
// into rollback.go so the rollback-table is documented + tested in
// isolation. The forward declaration here lets Task 15's commit be
// self-contained.
func (h *ScheduledAgentHandler) rollbackStage4Failure(result *worktree.CreateResult) {
	_, _ = h.deps.Worktree.Destroy(context.Background(), worktree.DestroyOpts{
		RepoPath:     h.deps.RepoPath,
		WorktreePath: result.Path,
		Branch:       result.Branch,
		Force:        true,
	})
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
		return err
	case errors.Is(err, worktree.ErrPathExists):
		_ = reporter.Transition(scheduler.StateFailed,
			"stage 3a: stale worktree at expected path; queued for next-boot sweep",
			map[string]any{"original_err": err.Error()})
		return err
	case errors.Is(err, worktree.ErrPersistentBranchMismatch):
		_ = reporter.Transition(scheduler.StateFailed,
			"stage 3a: persistent worktree squatted by operator-owned branch; manual reconciliation required",
			map[string]any{"original_err": err.Error()})
		return err
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
// recovery. Per spec §7.7 the real body lives in E6.9; E6.1 ships
// this stub so the Handler interface is satisfied.
//
// The semantics E6.9 will fill in: enumerate non-terminal runs at
// boot, classify each (resumable worktree intact, terminal-failed,
// lost-track), and return the resolved state so the substrate can
// advance scheduler_job_state.
func (h *ScheduledAgentHandler) Reconcile(ctx context.Context, job scheduler.JobSpec, runID string, lastState scheduler.State) (scheduler.State, error) {
	// TODO(thrum-6qmf.4.63): delegate to E6.9 ReconcileRun.
	return lastState, nil
}
