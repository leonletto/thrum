package cli

import "fmt"

func init() {
	RegisterHintSource("tmux.create", tmuxCreateHints)
}

// TmuxCreateResultMarker is the shape the command site stamps into
// HintCtx.Result so the post-action hint source can detect stale-identity
// replacement without re-reading (now-mutated) worktree state. The command
// site sets ReplacedStaleIdentity=true only when --force caused overwrite
// of an IdentityStale worktree; ReplacedAgentName carries the previous
// agent name for the audit-trail hint.
type TmuxCreateResultMarker struct {
	ReplacedStaleIdentity bool
	ReplacedAgentName     string
}

func tmuxCreateHints(ctx HintCtx) []Hint {
	cwd, _ := ctx.Flags["cwd"].(string)
	force, _ := ctx.Flags["force"].(bool)
	var out []Hint

	if !ctx.Post {
		return tmuxCreatePreHints(ctx, cwd, out)
	}
	return tmuxCreatePostHints(ctx, cwd, force, out)
}

func tmuxCreatePreHints(ctx HintCtx, cwd string, out []Hint) []Hint {
	// 1. not-a-worktree (hard refusal). When cwd is not a worktree we
	// short-circuit — checking identity of a non-worktree makes no sense
	// and would just produce confusing downstream noise.
	if cwd != "" && ctx.State != nil {
		ok, err := ctx.State.IsGitWorktree(cwd)
		if err == nil && !ok {
			return append(out, Hint{
				Code:     HintTmuxCreateNotAWorktree,
				Severity: SeverityWarn,
				Message:  fmt.Sprintf("--cwd '%s' is not a git worktree", cwd),
				Options: []Option{
					{Label: "fix", Cmd: fmt.Sprintf("git worktree add %s <branch>", cwd)},
					{Label: "or", Cmd: "thrum worktree create <name>", Note: "creates + wires a fresh worktree"},
				},
				AllowForce: false,
			})
		}
	}

	// 2. session-exists (recoverable via --force — replaces existing session).
	if len(ctx.Args) > 0 && ctx.State != nil {
		name := ctx.Args[0]
		if alive, err := ctx.State.TmuxSessionExists(name); err == nil && alive {
			out = append(out, Hint{
				Code:     HintTmuxCreateSessionExists,
				Severity: SeverityWarn,
				Message:  fmt.Sprintf("tmux session '%s' already running", name),
				Options: []Option{
					{Label: "attach", Cmd: fmt.Sprintf("thrum tmux connect %s", name)},
					{Label: "replace", Cmd: "thrum tmux create --force", Note: "kills existing session"},
				},
				AllowForce: true,
			})
		}
	}

	// 3a/3b. identity-exists (alive = hard refusal, stale = recoverable).
	if cwd != "" && ctx.State != nil {
		if status, agent, err := ctx.State.IdentityStatus(cwd); err == nil {
			atName := ""
			agentID := ""
			if agent != nil {
				agentID = agent.AgentID
				atName = "@" + agentID
			}
			switch status {
			case IdentityLive:
				out = append(out, Hint{
					Code:     HintTmuxCreateIdentityExistsAlive,
					Severity: SeverityWarn,
					Message:  fmt.Sprintf("worktree has live agent %s", atName),
					Options: []Option{
						{Label: "rename", Cmd: fmt.Sprintf("ask %s to rename or end its session first", atName)},
						{Label: "attach", Cmd: fmt.Sprintf("thrum tmux connect %s", agentID)},
					},
					AllowForce: false,
				})
			case IdentityStale:
				out = append(out, Hint{
					Code:     HintTmuxCreateIdentityExistsStale,
					Severity: SeverityWarn,
					Message:  fmt.Sprintf("worktree has stale identity %s (no live session)", atName),
					Options: []Option{
						{Label: "resume", Cmd: fmt.Sprintf("thrum tmux connect %s", agentID), Note: "if session recoverable"},
						{Label: "replace", Cmd: "thrum tmux create --force", Note: "overwrites stale identity"},
					},
					AllowForce: true,
				})
			}
		}
	}

	return out
}

func tmuxCreatePostHints(ctx HintCtx, cwd string, force bool, out []Hint) []Hint {
	_ = cwd // reserved for future post-action checks; keeps parallelism with pre path
	name := ""
	if len(ctx.Args) > 0 {
		name = ctx.Args[0]
	}

	// 4. next-launch (always fires on success). Encodes R-15 steps 3–4 and
	// R-16 (kiro/auggie don't auto-prime).
	out = append(out, Hint{
		Code:     HintTmuxCreateNextLaunch,
		Severity: SeverityInfo,
		Message:  "session created — agent is NOT running yet",
		Options: []Option{
			{Label: "start", Cmd: fmt.Sprintf("thrum tmux launch %s", name)},
			{Label: "prime", Cmd: fmt.Sprintf("thrum tmux send %s '/thrum:prime'", name), Note: "after launch, for non-claude runtimes"},
		},
	})

	// 5. identity-replaced (audit trail; fires only when --force caused a
	// stale-identity overwrite, detected via Result marker set by the
	// command site — post-replacement IdentityStatus() can't distinguish
	// "just replaced" from "never had identity").
	if force {
		if marker, ok := ctx.Result.(TmuxCreateResultMarker); ok && marker.ReplacedStaleIdentity {
			out = append(out, Hint{
				Code:     HintTmuxCreateIdentityReplaced,
				Severity: SeverityInfo,
				Message:  fmt.Sprintf("replaced stale identity (previously @%s)", marker.ReplacedAgentName),
				Options: []Option{
					{Label: "next", Cmd: fmt.Sprintf("thrum tmux launch %s", name)},
				},
			})
		}
	}

	return out
}
