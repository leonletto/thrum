package hookmerge

import (
	"encoding/json"
	"fmt"
	"os"
)

// MergeClaudeSettings reconciles thrum's canonical hook commands (extracted
// from `renderedTemplate`) into the .claude/settings.json file at `outPath`.
//
// Behavior:
//
//   - File does not exist  → writes `renderedTemplate` verbatim.
//   - File exists, force=true → overwrites with `renderedTemplate` verbatim.
//   - File exists, force=false → loads existing settings, walks every
//     (event, command) pair in `renderedTemplate`, ensures each is present
//     via AddHookCommand (idempotent), then saves the merged map.
//
// Third-party entries (bd hooks, user-customized commands, other tool entries)
// are preserved. Save uses 2-space indent so the output matches `bd setup
// claude`'s format and re-running on an already-current file produces no
// diff.
//
// This function intentionally does NOT remove thrum hooks that no longer
// appear in `renderedTemplate`. That direction (template change drops an
// old hook) is rare and would require a separate migration tied to a
// specific schema bump.
func MergeClaudeSettings(outPath, renderedTemplate string, force bool) (MergeResult, error) {
	merged, action, err := computeClaudeMerge(outPath, renderedTemplate, force)
	if err != nil {
		return MergeResult{}, err
	}
	// Skip the write when nothing changed semantically. Acceptance #3
	// requires "no diff" — that includes the file's mtime, since IDEs
	// and git-status-by-mtime heuristics treat an mtime bump as a dirty
	// file. The byte content would already be stable thanks to
	// Save's deterministic format, but staying off the inode keeps the
	// noop truly observable as a noop.
	if action.Action == "noop" {
		return action, nil
	}
	if err := Save(outPath, merged); err != nil {
		return MergeResult{}, err
	}
	return action, nil
}

// PreviewClaudeMerge reports what MergeClaudeSettings WOULD do without
// writing the file. Used by dry-run callers (e.g. `thrum init --dry-run`)
// so the action label ("create" / "overwrite" / "merge" / "noop") reflects
// the actual semantic decision instead of a stat-only guess.
func PreviewClaudeMerge(outPath, renderedTemplate string, force bool) (MergeResult, error) {
	_, action, err := computeClaudeMerge(outPath, renderedTemplate, force)
	return action, err
}

// computeClaudeMerge is the shared computation backing both
// MergeClaudeSettings (write path) and PreviewClaudeMerge (dry-run).
// Returns the merged settings map plus the action label without touching
// disk.
func computeClaudeMerge(outPath, renderedTemplate string, force bool) (Settings, MergeResult, error) {
	var rendered Settings
	if err := json.Unmarshal([]byte(renderedTemplate), &rendered); err != nil {
		return nil, MergeResult{}, fmt.Errorf("parse rendered template: %w", err)
	}

	info, statErr := os.Stat(outPath)
	exists := statErr == nil && !info.IsDir()

	if !exists {
		return rendered, MergeResult{Action: "create"}, nil
	}
	if force {
		return rendered, MergeResult{Action: "overwrite"}, nil
	}

	existing, err := Load(outPath)
	if err != nil {
		return nil, MergeResult{}, fmt.Errorf("load existing settings: %w", err)
	}

	thrumCommands := ExtractCommands(rendered)
	addedAny := false
	for _, ec := range thrumCommands {
		if AddHookCommand(existing, ec.Event, ec.Command) {
			addedAny = true
		}
	}
	if addedAny {
		return existing, MergeResult{Action: "merge"}, nil
	}
	return existing, MergeResult{Action: "noop", AlreadyCurrent: true}, nil
}

// MergeResult reports what MergeClaudeSettings did (or PreviewClaudeMerge
// would do) to the target file. The name is distinct from cli.FileAction —
// the cli type carries presentation fields (Path, Runtime, Template) that
// the hookmerge package neither needs nor should know about.
type MergeResult struct {
	// Action is "create" (new file), "overwrite" (force), "merge" (added
	// at least one hook), or "noop" (every thrum hook was already present).
	Action string
	// AlreadyCurrent is true when no merge add was needed — the file was
	// byte-stable across the merge.
	AlreadyCurrent bool
}

// CanonicalThrumHooks enumerates thrum's owned hook commands in
// .claude/settings.json. The template under
// internal/cli/templates/claude/settings.json.tmpl renders the same set —
// runtime-init's TestClaudeTemplateMatchesCanonicalHooks asserts they stay
// in sync, so any drift fails CI rather than diverging silently.
//
// Both code paths converge through AddHookCommand: cli/runtime-init in the
// main repo, worktree.EnsureRedirects per worktree.
var CanonicalThrumHooks = []EventCommand{
	{Event: "SessionStart", Command: `bash "${CLAUDE_PROJECT_DIR}/scripts/thrum-startup.sh"`},
	{Event: "Stop", Command: `HOOK_EVENT=Stop bash "${CLAUDE_PROJECT_DIR}/scripts/thrum-check-inbox.sh"`},
	{Event: "PostToolUse", Command: `HOOK_EVENT=PostToolUse bash "${CLAUDE_PROJECT_DIR}/scripts/thrum-check-inbox.sh"`},
	{Event: "UserPromptSubmit", Command: `HOOK_EVENT=UserPromptSubmit bash "${CLAUDE_PROJECT_DIR}/scripts/thrum-check-inbox.sh"`},
	{Event: "PreCompact", Command: `HOOK_EVENT=PreCompact bash "${CLAUDE_PROJECT_DIR}/scripts/thrum-check-inbox.sh"`},
}

// InstallThrumClaudeHooks ensures every CanonicalThrumHook is present in
// settings.json at the given path. Existing entries (third-party hooks,
// user customizations) are preserved; missing thrum hooks are appended.
// Idempotent — re-running on an already-current file produces no diff.
//
// Use this from callers that lack a rendered template (worktree.EnsureRedirects).
// cli/runtime-init in the main repo uses MergeClaudeSettings with the
// rendered template content so the dry-run summary can report the right
// action label.
func InstallThrumClaudeHooks(settingsPath string) error {
	settings, err := Load(settingsPath)
	if err != nil {
		return fmt.Errorf("load %s: %w", settingsPath, err)
	}
	for _, ec := range CanonicalThrumHooks {
		AddHookCommand(settings, ec.Event, ec.Command)
	}
	return Save(settingsPath, settings)
}
