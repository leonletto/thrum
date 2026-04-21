package cli

import "fmt"

// Snapshot-save hints differ structurally from tmux.create / init hints: they
// fire on error return paths, not pre/post detection. We therefore do NOT
// register them via RegisterHintSource — instead the command site calls these
// pure builders when it knows it is returning an error and wants to attach a
// remediation trailer via EmitStderr. Keeping them as free-standing Hint
// constructors (rather than a HintSource) means they are trivially unit-testable
// with no HintCtx fixtures and make the error-attachment pattern obvious at
// the call site.

// SnapshotSaveNoJSONLHint is emitted when all three JSONL resolution layers
// failed (PID-based lookup, mtime fallback against the worktree project dir,
// and no explicit --jsonl path). Common causes:
//   - agent is running under a non-Claude runtime
//   - Claude rotated the project directory (session PID no longer current)
//   - identity file's agent_pid is stale and points at a dead process
//   - worktree path doesn't match Claude's project-slug encoding
func SnapshotSaveNoJSONLHint(pid int, claudeDir string) Hint {
	return Hint{
		Code:     HintSnapshotSaveNoJSONL,
		Severity: SeverityWarn,
		Message:  fmt.Sprintf("no Claude JSONL found for PID %d under %s (PID lookup + mtime fallback both failed)", pid, claudeDir),
		Options: []Option{
			{Label: "locate", Cmd: fmt.Sprintf("ls %s/projects/ | grep -i <worktree-dir>", claudeDir), Note: "find the encoded project slug for your worktree"},
			{Label: "override", Cmd: "thrum tmux snapshot save --jsonl <path>", Note: "supply the JSONL path directly (bypass auto-detect)"},
			{Label: "verify-pid", Cmd: fmt.Sprintf("ls %s/sessions/%d.json", claudeDir, pid), Note: "confirm whether Claude recorded the agent's session"},
			{Label: "re-register", Cmd: "thrum quickstart --name <name> --role <role> --module <module> --runtime claude --force", Note: "if agent_pid is stale"},
			{Label: "check-runtime", Cmd: "thrum whoami", Note: "confirm runtime=claude; snapshot save only supports claude today"},
		},
	}
}

// SnapshotSaveNoPIDHint is emitted when the save command could not resolve
// the agent's PID — neither the identity file nor the daemon's agent.list
// returned a non-zero AgentPID for the agent name.
func SnapshotSaveNoPIDHint(agentName string) Hint {
	return Hint{
		Code:     HintSnapshotSaveNoPID,
		Severity: SeverityWarn,
		Message:  fmt.Sprintf("no agent PID resolvable for %s (identity file and daemon both returned 0)", agentName),
		Options: []Option{
			{Label: "re-register", Cmd: fmt.Sprintf("thrum quickstart --name %s --role <role> --module <module> --runtime claude --force", agentName), Note: "writes current PID into identity file"},
			{Label: "verify-daemon", Cmd: "thrum status"},
		},
	}
}

// SnapshotSaveExtractFailedHint is emitted when restart.ExtractConversation
// failed — the JSONL path resolved but reading or parsing it errored.
// Typically permissions or file-corruption issues.
func SnapshotSaveExtractFailedHint(jsonlPath string) Hint {
	return Hint{
		Code:     HintSnapshotSaveExtractFailed,
		Severity: SeverityWarn,
		Message:  fmt.Sprintf("failed to extract conversation from %s", jsonlPath),
		Options: []Option{
			{Label: "inspect", Cmd: fmt.Sprintf("head -5 %s", jsonlPath)},
			{Label: "check-perms", Cmd: fmt.Sprintf("ls -l %s", jsonlPath)},
			{Label: "check-format", Cmd: fmt.Sprintf("wc -l %s", jsonlPath), Note: "empty or zero-line file signals truncation"},
		},
	}
}
