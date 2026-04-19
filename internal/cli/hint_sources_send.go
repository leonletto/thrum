package cli

import (
	"fmt"
	"strings"
	"time"
)

func init() {
	RegisterHintSource("send", sendHints)
}

// sendHints emits `send.recipient-stale` when a single named recipient has
// been inactive beyond RecipientStaleMinutes. Info severity (never blocks);
// pilot has no post-action send hints.
func sendHints(ctx HintCtx) []Hint {
	if ctx.Post {
		return nil // pilot: no post-action send hints
	}

	to, _ := ctx.Flags["to"].(string)
	if to == "" {
		return nil
	}
	name := strings.TrimPrefix(to, "@")
	if name == "" {
		return nil
	}

	if ctx.State == nil {
		return nil
	}
	agent, err := ctx.State.AgentByName(name)
	if err != nil || agent == nil {
		// Error or unknown recipient — error path of cli.Send owns this.
		return nil
	}

	if agent.UpdatedAt == "" {
		return nil
	}
	last, err := time.Parse(time.RFC3339, agent.UpdatedAt)
	if err != nil {
		return nil // best-effort
	}
	since := time.Since(last)
	if since <= RecipientStaleThreshold {
		return nil
	}

	// Prefer the agent's real tmux session name when the daemon gave it to
	// us; fall back to the angle-bracket template (spec §4 example shape)
	// so the hint is still actionable for operators who don't have the
	// session name handy.
	sessionArg := fmt.Sprintf("<%s-session>", name)
	if agent.TmuxSession != "" {
		sessionArg = agent.TmuxSession
	}
	minutes := int(since.Minutes())
	return []Hint{{
		Code:     HintSendRecipientStale,
		Severity: SeverityInfo,
		Message:  fmt.Sprintf("@%s last seen %dm ago — may be idle", name, minutes),
		Options: []Option{
			{Label: "nudge", Cmd: fmt.Sprintf("thrum ping @%s", name)},
			{Label: "reprime", Cmd: fmt.Sprintf("thrum tmux send %s '/thrum:prime'", sessionArg)},
		},
	}}
}
