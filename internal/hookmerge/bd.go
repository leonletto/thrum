package hookmerge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// CanonicalBdCommand is the exact SessionStart hook command thrum manages
// on behalf of bd (beads). bd setup claude also writes this exact string;
// the two sources of truth must match for idempotency to hold across
// alternating invocations of bd setup claude and thrum init.
const CanonicalBdCommand = "bd prime --hook-json"

// legacyBdCmd is one entry in the migration sweep: a bd command variant to
// remove from a given hook event. An empty event matches all events.
type legacyBdCmd struct {
	event   string // "" matches all events
	command string
}

// legacyBdCommandsFor returns the migration-sweep list appropriate for the
// installed bd's --hook-json capability. The list never strips the command
// InstallBdHook is about to (re-)add on SessionStart, so the canonical hook
// survives the sweep and the operation stays idempotent.
//
// supportsHookJSON=true  → emit "bd prime --hook-json"; the bare "bd prime" is
//
//	the pre-hook-json legacy shape and is stripped from every event. (Byte-
//	identical to the historical behavior.)
//
// supportsHookJSON=false → the installed bd rejects --hook-json, so emit bare
//
//	"bd prime": strip the broken "bd prime --hook-json" from every event (incl.
//	SessionStart, replacing it with the working bare form), keep bare
//	"bd prime" on SessionStart, and strip bare "bd prime" only from PreCompact
//	(SessionStart now fires post-compaction).
func legacyBdCommandsFor(supportsHookJSON bool) []legacyBdCmd {
	if supportsHookJSON {
		return []legacyBdCmd{
			// Remove the bare `bd prime` and stealth variants from any event —
			// they're the pre-hook-json shape and never wanted post-migration.
			{event: "", command: "bd prime"},
			{event: "", command: "bd prime --stealth"},
			// PreCompact-specific removals: the hook-json variants ARE valid on
			// SessionStart, but should be cleaned up from PreCompact since
			// SessionStart now also fires after compaction.
			{event: "PreCompact", command: CanonicalBdCommand},
			{event: "PreCompact", command: "bd prime --stealth --hook-json"},
		}
	}
	return []legacyBdCmd{
		// Stale bd: bare "bd prime" is canonical, so it must survive on
		// SessionStart. Strip the broken --hook-json forms everywhere, and
		// strip bare "bd prime" only from PreCompact.
		{event: "", command: "bd prime --stealth"},
		{event: "", command: "bd prime --hook-json"},
		{event: "", command: "bd prime --stealth --hook-json"},
		{event: "PreCompact", command: "bd prime"},
	}
}

// BdBinaryAvailable reports whether `bd --version` exits 0 — used by
// runtime-init to decide the default for Worktrees.BeadsEnabled. Returns
// false when the binary is missing, on PATH but broken, or times out.
//
// The 2s timeout protects against hung binaries; bd's own --version is
// purely local and returns near-instantly when working.
//
// Exposed as a function-typed variable so tests can stub it without
// depending on the CI host having bd installed.
var BdBinaryAvailable = func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Fixed binary name + literal arg, no user input.
	cmd := exec.CommandContext(ctx, "bd", "--version") //#nosec G204
	return cmd.Run() == nil
}

// BdSupportsHookJSON reports whether the installed bd's `prime` subcommand
// accepts the --hook-json flag. It parses `bd prime --help` output for the
// flag string (no side effects) rather than invoking the flag — and detects
// the FLAG specifically, not merely that bd exists (an old bd 1.0.4 exits 0 on
// --help too; binary presence is BdBinaryAvailable's job). Returns false when
// the binary is missing, broken, times out, or lacks the flag.
//
// Released bd 1.0.4 has no --hook-json; the flag exists only on unreleased bd
// HEAD. This probe lets InstallBdHook prefer "bd prime --hook-json" whenever bd
// supports it and fall back to bare "bd prime" otherwise.
//
// Exposed as a function-typed variable so tests can stub it without depending
// on the host's bd version. Single-purpose by design so thrum-gxwk can later
// generalize capability-probing without touching call sites.
var BdSupportsHookJSON = func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Fixed binary name + literal args, no user input.
	cmd := exec.CommandContext(ctx, "bd", "prime", "--help") //#nosec G204
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return bytes.Contains(out, []byte("--hook-json"))
}

// InstallBdHookOptions configures InstallBdHook. The zero value disables
// every guard (bd hook is unconditionally installed) — production callers
// populate every field.
type InstallBdHookOptions struct {
	// SettingsPath is the absolute path to the project's .claude/settings.json.
	// The file is read, mutated, and written by InstallBdHook.
	SettingsPath string

	// LocalSettingsPath is the absolute path to .claude/settings.local.json.
	// If non-empty and the file exists, InstallBdHook strips any legacy
	// bd hooks from it as part of the same call.
	LocalSettingsPath string

	// PluginGuardPaths lists additional settings files to consult for the
	// "beads plugin enabled" gate. If any file in this list has
	// enabledPlugins.beads=true, the bd hook install is SKIPPED to avoid
	// double-fire with the marketplace plugin. Project SettingsPath itself
	// is always checked.
	PluginGuardPaths []string
}

// InstallBdResult describes what InstallBdHook did to the file. Surfaces
// to runtime-init / hint messaging.
type InstallBdResult struct {
	// Skipped is true if the beads plugin was detected enabled (in any
	// of the configured settings files) — no file write occurred.
	Skipped bool

	// SkippedReason is a human-readable explanation when Skipped is true.
	SkippedReason string

	// Added is true if InstallBdHook appended the canonical bd hook (i.e.
	// it wasn't already present).
	Added bool

	// LegacyRemoved is the count of legacy bd entries the migration sweep
	// removed (across all events in the project settings file).
	LegacyRemoved int

	// LocalMigrated is true if .claude/settings.local.json had legacy bd
	// hooks stripped during this call.
	LocalMigrated bool
}

// InstallBdHook merges the canonical bd SessionStart hook into the project
// settings file at opts.SettingsPath. The operation:
//
//  1. Loads the project settings file (creating an empty map if absent).
//  2. Checks every plugin-guard settings file (and the project file) for
//     `enabledPlugins.beads=true`. If found, returns Skipped without touching
//     the file — the marketplace plugin owns the hook.
//  3. Runs the migration sweep: removes legacy bd command variants from every
//     event in the project file.
//  4. Adds the canonical SessionStart hook if not already present.
//  5. Saves the file (always, even if no changes — ensures byte-stable format).
//  6. Migrates the legacy `.claude/settings.local.json` file if present and
//     populated, stripping bd hooks.
//
// Idempotent: re-running on an already-current file produces no semantic
// change (and byte-stable output thanks to the alphabetical-key MarshalIndent
// format in Save).
func InstallBdHook(opts InstallBdHookOptions) (InstallBdResult, error) {
	if opts.SettingsPath == "" {
		return InstallBdResult{}, fmt.Errorf("InstallBdHook: SettingsPath is required")
	}

	projectSettings, err := Load(opts.SettingsPath)
	if err != nil {
		return InstallBdResult{}, fmt.Errorf("load project settings: %w", err)
	}

	// Guard: skip entirely if the beads marketplace plugin is detected
	// enabled in the project file or any of the configured plugin-guard
	// paths (typically ~/.claude/settings.json + .claude/settings.local.json).
	if HasBeadsPlugin(projectSettings) {
		return InstallBdResult{
			Skipped:       true,
			SkippedReason: fmt.Sprintf("beads plugin enabled in %s", opts.SettingsPath),
		}, nil
	}
	for _, guard := range opts.PluginGuardPaths {
		if guard == "" {
			continue
		}
		s, err := Load(guard)
		if err != nil {
			// Don't let a malformed sibling-settings file block install;
			// log via return value rather than failing the whole pass.
			// Empty map -> HasBeadsPlugin returns false naturally.
			continue
		}
		if HasBeadsPlugin(s) {
			return InstallBdResult{
				Skipped:       true,
				SkippedReason: fmt.Sprintf("beads plugin enabled in %s", guard),
			}, nil
		}
	}

	// Capability-aware command selection. Released bd 1.0.4 lacks --hook-json,
	// so emit the bare form there; prefer "bd prime --hook-json" whenever bd
	// supports it. The sweep list is derived from the same probe so it never
	// strips the command we're about to add (preserves idempotency).
	supportsHookJSON := BdSupportsHookJSON()
	canonical := "bd prime"
	if supportsHookJSON {
		canonical = CanonicalBdCommand
	}

	// Migration sweep across events for every legacy variant.
	result := InstallBdResult{}
	for _, lr := range legacyBdCommandsFor(supportsHookJSON) {
		if lr.event != "" {
			if RemoveHookCommand(projectSettings, lr.event, lr.command) {
				result.LegacyRemoved++
			}
			continue
		}
		// Empty event → sweep every event in the file. Snapshot the
		// event list first since RemoveHookCommand mutates the parent map.
		var events []string
		if hooks, ok := projectSettings["hooks"].(map[string]any); ok {
			for e := range hooks {
				events = append(events, e)
			}
		}
		for _, e := range events {
			if RemoveHookCommand(projectSettings, e, lr.command) {
				result.LegacyRemoved++
			}
		}
	}

	// Add canonical hook (idempotent).
	if AddHookCommand(projectSettings, "SessionStart", canonical) {
		result.Added = true
	}

	// Skip the write when nothing changed semantically (no add, no
	// sweep). Re-running thrum init on a current file MUST NOT bump
	// the mtime — IDEs and git-status-by-mtime heuristics treat that
	// as a dirty file (acceptance #3).
	if result.Added || result.LegacyRemoved > 0 {
		if err := Save(opts.SettingsPath, projectSettings); err != nil {
			return result, fmt.Errorf("save project settings: %w", err)
		}
	}

	// Legacy settings.local.json migration: strip every bd command
	// variant from every event in the legacy file. No-op if absent.
	if opts.LocalSettingsPath != "" {
		migrated, err := migrateSettingsLocal(opts.LocalSettingsPath)
		if err != nil {
			return result, fmt.Errorf("migrate legacy settings.local.json: %w", err)
		}
		result.LocalMigrated = migrated
	}

	return result, nil
}

// migrateSettingsLocal strips every variant of bd's SessionStart/PreCompact
// hooks (including the canonical hook-json form) from
// .claude/settings.local.json. The legacy file is no longer thrum-managed;
// removing bd hooks there prevents double-fire with the current
// .claude/settings.json install. Returns true if any hook was removed.
func migrateSettingsLocal(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false, nil //nolint:nilerr // missing file is expected/non-fatal
	}

	settings, err := Load(path)
	if err != nil {
		return false, err
	}

	// Strip every known bd command variant from every event.
	allVariants := []string{
		"bd prime",
		"bd prime --stealth",
		CanonicalBdCommand,
		"bd prime --stealth --hook-json",
	}
	removedAny := false

	var events []string
	if hooks, ok := settings["hooks"].(map[string]any); ok {
		for e := range hooks {
			events = append(events, e)
		}
	}
	for _, e := range events {
		for _, cmd := range allVariants {
			if RemoveHookCommand(settings, e, cmd) {
				removedAny = true
			}
		}
	}

	if removedAny {
		if err := Save(path, settings); err != nil {
			return false, err
		}
	}
	return removedAny, nil
}

// DefaultGuardPaths returns the conventional list of plugin-guard settings
// paths thrum should consult when deciding whether to install the bd hook:
// the user's global ~/.claude/settings.json plus the project's
// settings.local.json. Callers typically pass these into
// InstallBdHookOptions.PluginGuardPaths.
//
// homeDir is the user's home directory (typically os.UserHomeDir()).
// projectDir is the project root (the parent of .claude/).
func DefaultGuardPaths(homeDir, projectDir string) []string {
	var paths []string
	if homeDir != "" {
		paths = append(paths, filepath.Join(homeDir, ".claude", "settings.json"))
	}
	if projectDir != "" {
		paths = append(paths, filepath.Join(projectDir, ".claude", "settings.local.json"))
	}
	return paths
}
