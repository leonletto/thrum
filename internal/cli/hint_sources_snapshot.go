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

// SnapshotSaveNoJSONLContext lets the command site explain WHY auto-detect
// failed so the rendered hint can be specific about the root cause. All
// fields are optional; leaving them zero produces the generic catchall
// message. The three fields are mutually exclusive in practice but aren't
// enforced so — if ever needed — a call site can layer context.
type SnapshotSaveNoJSONLContext struct {
	// WorktreeMissing is true when idFile.Worktree was empty and the mtime
	// fallback therefore could not be attempted at all. Drives the operator
	// toward re-registering the agent with a populated worktree field.
	WorktreeMissing bool
	// ProjectDirReadErr is the error returned by FindLatestJSONLForCwd when
	// the project dir itself could not be read (ENOENT, permission denied,
	// etc). Distinct from an empty project dir — see ProjectDirEmpty.
	ProjectDirReadErr error
	// ProjectDirEmpty is true when the project dir exists but holds no
	// .jsonl files. Distinct from a read error — Claude may have rotated.
	ProjectDirEmpty bool
}

// SnapshotSaveNoJSONLHint is emitted when all three JSONL resolution layers
// failed (PID-based lookup, mtime fallback against the worktree project dir,
// and no explicit --jsonl path). Common causes:
//   - agent is running under a non-Claude runtime
//   - Claude rotated the project directory (session PID no longer current)
//   - identity file's agent_pid is stale and points at a dead process
//   - worktree path doesn't match Claude's project-slug encoding
//   - identity file's worktree field is empty (mtime fallback disabled)
//
// Context is optional; pass the zero value to keep the generic message.
func SnapshotSaveNoJSONLHint(pid int, claudeDir string, ctx SnapshotSaveNoJSONLContext) Hint {
	msg := fmt.Sprintf("no Claude JSONL found for PID %d under %s (PID lookup + mtime fallback both failed)", pid, claudeDir)
	switch {
	case ctx.WorktreeMissing:
		msg = fmt.Sprintf("no Claude JSONL found for PID %d under %s (mtime fallback skipped — identity file is missing the 'worktree' field)", pid, claudeDir)
	case ctx.ProjectDirReadErr != nil:
		msg = fmt.Sprintf("no Claude JSONL found for PID %d under %s (project dir lookup failed: %v)", pid, claudeDir, ctx.ProjectDirReadErr)
	case ctx.ProjectDirEmpty:
		msg = fmt.Sprintf("no Claude JSONL found for PID %d under %s (project dir exists but contains no .jsonl files)", pid, claudeDir)
	}
	return Hint{
		Code:     HintSnapshotSaveNoJSONL,
		Severity: SeverityWarn,
		Message:  msg,
		Options: []Option{
			{Label: "locate", Cmd: fmt.Sprintf("ls \"%s/projects/\" | grep -i <worktree-dir>", claudeDir), Note: "find the encoded project slug for your worktree"},
			{Label: "override", Cmd: "thrum tmux snapshot save --jsonl <path>", Note: "supply the JSONL path directly (bypass auto-detect)"},
			{Label: "verify-pid", Cmd: fmt.Sprintf("ls \"%s/sessions/%d.json\"", claudeDir, pid), Note: "confirm whether Claude recorded the agent's session"},
			{Label: "re-register", Cmd: "thrum quickstart --name <name> --role <role> --module <module> --runtime claude --force", Note: "if agent_pid is stale or worktree is missing"},
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
			{Label: "inspect", Cmd: fmt.Sprintf("head -5 \"%s\"", jsonlPath)},
			{Label: "check-perms", Cmd: fmt.Sprintf("ls -l \"%s\"", jsonlPath)},
			{Label: "check-format", Cmd: fmt.Sprintf("wc -l \"%s\"", jsonlPath), Note: "empty or zero-line file signals truncation"},
		},
	}
}

// SnapshotSaveJSONLNotFoundHint is emitted when --jsonl <path> was supplied
// but os.Stat reports the path does not exist. Distinct from
// SnapshotSaveExtractFailedHint so the remediation focuses on the typo /
// resolution case rather than permissions or corruption.
func SnapshotSaveJSONLNotFoundHint(jsonlPath string) Hint {
	return Hint{
		Code:     HintSnapshotSaveJSONLNotFound,
		Severity: SeverityWarn,
		Message:  fmt.Sprintf("--jsonl path not found: %s", jsonlPath),
		Options: []Option{
			{Label: "verify", Cmd: fmt.Sprintf("ls -l \"%s\"", jsonlPath), Note: "confirm the exact path (typo? symlink?)"},
			{Label: "locate", Cmd: "ls \"$HOME/.claude/projects/\"", Note: "browse Claude's project dirs to find the right JSONL"},
			{Label: "auto-detect", Cmd: "thrum tmux snapshot save", Note: "drop --jsonl to let auto-detect + mtime fallback try"},
		},
	}
}
