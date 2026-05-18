package agentdispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AgentRenderState carries the per-agent context the body-fallback
// chain consults to pick the "what's happening" line for the
// expanded `thrum team @<name>` view per spec §7.6. The team handler
// populates this from scheduler_job_state + the agent's tmux/journal
// surface, then hands it to RenderBodyFallbackChain.
type AgentRenderState struct {
	// JobCurrentState is the scheduler_job_state.current_state for
	// the agent's associated scheduled_agent job (empty for personal
	// agents). When set to "running", branch 1 fires (live pane).
	JobCurrentState string

	// ActiveJobID is the job spec ID the agent is currently running.
	// Surfaced inline next to the live-pane snippet so operators
	// can pivot to `thrum cron history <job_id>` from the team view.
	ActiveJobID string

	// Elapsed is the time the agent has spent in the current
	// running stage. Surfaced alongside the snippet.
	Elapsed time.Duration

	// LastCompletedAt is the last terminal-state timestamp for the
	// agent's job. Branch 2 uses this as the staleness gate on
	// summary.md (file must have mtime AFTER this to be rendered;
	// older summary.md is treated as a stale leftover from a prior
	// job run per IMPORTANT #3 from spec dual-review).
	LastCompletedAt time.Time

	// LastRunState is the canonical scheduler-state vocabulary for
	// the last terminal completion ("completed", "failed",
	// "cancelled"). Used by branch 4's "No summary" fallback line.
	LastRunState string
}

// OutboundMessage is the minimal shape RenderBodyFallbackChain
// consumes from MessageLookup.LastOutboundFromAgent. Keeps the
// renderer free of the message-handler's broader response shape.
type OutboundMessage struct {
	MessageID string
	Subject   string
}

// PaneCaptureFunc captures the last N lines from the agent's tmux
// pane for branch 1 of the fallback chain. Returns the captured
// text + nil on success; any error short-circuits branch 1 (the
// chain falls through to branch 2). Concrete production wiring is
// rpc.TmuxHandler's capture-pane path, adapted at the team handler.
type PaneCaptureFunc func(ctx context.Context, agentName string, lines int) (string, error)

// OutboundLookupFunc returns the most recent outbound-dispatched
// message FROM the agent (per spec §7.6 branch 3 — what the agent
// said, not what someone said to it). Returns nil when the agent
// has never sent a message. Production wiring runs the canonical
// outbound-by-author query against message_deliveries; tests
// inject fakes.
type OutboundLookupFunc func(ctx context.Context, agentName string) (*OutboundMessage, error)

// RenderBodyFallbackChain implements the spec §7.6 fallback chain
// for the `thrum team @<name>` expanded view's "what's happening"
// section. Four branches in fixed recency-ranked order:
//
//  1. If state.JobCurrentState == "running" AND PaneCapture
//     succeeds → live pane snippet + active job id + elapsed.
//  2. Else if summary.md exists AND mtime > state.LastCompletedAt
//     → render the file content.
//  3. Else if OutboundLookup returns a message → "Last said:
//     <message_id> · <subject>".
//  4. Else → "Last run: <state> at <time>. No summary."
//
// agentsDir is `.thrum/agents/` (the per-agent state-folder root);
// agentName joins onto it to reach the summary.md path. capture +
// outbound are nil-safe — when either dep isn't wired (e.g.,
// fixtures running without tmux), that branch is skipped and the
// chain falls through to the next.
//
// All errors are absorbed: branch 1 failing falls to branch 2,
// branch 2 failing falls to branch 3, etc. The "No summary"
// fallback (branch 4) is the only response that always succeeds.
func RenderBodyFallbackChain(
	ctx context.Context,
	agentName string,
	agentsDir string,
	state AgentRenderState,
	capture PaneCaptureFunc,
	outbound OutboundLookupFunc,
) string {
	// Branch 1: live pane snippet.
	if state.JobCurrentState == "running" && capture != nil {
		if snippet, err := capture(ctx, agentName, 10); err == nil && snippet != "" {
			return fmt.Sprintf("%s\n\n[active job: %s, elapsed: %s]",
				snippet, state.ActiveJobID, state.Elapsed.String())
		}
	}

	// Branch 2: summary.md (per spec §7.6 staleness gate — file
	// mtime must be strictly AFTER LastCompletedAt; equal-time
	// means the boundary case is excluded per Go's time.After
	// semantics. Documented at the call site so a future
	// reviewer who expects inclusive bounds doesn't "fix" the
	// boundary into a stale-summary leak).
	if agentsDir != "" {
		summaryPath := filepath.Join(agentsDir, agentName, "summary.md")
		if info, err := os.Stat(summaryPath); err == nil {
			if info.ModTime().After(state.LastCompletedAt) {
				// #nosec G304 -- summaryPath is constrained to
				// agentsDir/<agentName>/summary.md where agentsDir is
				// daemon-internal config (.thrum/agents/) and agentName
				// comes from the agent registry, not user input.
				if content, err := os.ReadFile(summaryPath); err == nil && len(content) > 0 {
					return string(content)
				}
			}
		}
	}

	// Branch 3: most recent outbound message.
	if outbound != nil {
		if msg, err := outbound(ctx, agentName); err == nil && msg != nil && msg.MessageID != "" {
			return fmt.Sprintf("Last said: %s · %s", msg.MessageID, msg.Subject)
		}
	}

	// Branch 4: static "No summary" fallback. Always succeeds.
	if state.LastRunState == "" || state.LastCompletedAt.IsZero() {
		return "No summary."
	}
	return fmt.Sprintf("Last run: %s at %s. No summary.",
		state.LastRunState, state.LastCompletedAt.UTC().Format(time.RFC3339))
}
