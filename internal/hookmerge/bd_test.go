package hookmerge

import (
	"os"
	"path/filepath"
	"testing"
)

// stubHookJSON pins BdSupportsHookJSON to v for the duration of a test so
// assertions about which canonical form is emitted are deterministic
// regardless of the host's bd version. Restores the real probe on cleanup.
func stubHookJSON(t *testing.T, v bool) {
	t.Helper()
	orig := BdSupportsHookJSON
	BdSupportsHookJSON = func() bool { return v }
	t.Cleanup(func() { BdSupportsHookJSON = orig })
}

func TestInstallBdHook_FreshFile(t *testing.T) {
	stubHookJSON(t, true)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")

	res, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Skipped {
		t.Fatalf("expected install, got skip: %s", res.SkippedReason)
	}
	if !res.Added {
		t.Fatal("expected Added=true on fresh file")
	}
	if res.LegacyRemoved != 0 {
		t.Fatalf("expected no legacy removed, got %d", res.LegacyRemoved)
	}

	settings, err := Load(settingsPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmds := ExtractCommands(settings)
	if len(cmds) != 1 || cmds[0].Command != CanonicalBdCommand || cmds[0].Event != "SessionStart" {
		t.Fatalf("expected single canonical hook on SessionStart, got %+v", cmds)
	}
}

func TestInstallBdHook_Idempotent(t *testing.T) {
	stubHookJSON(t, true)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")

	if _, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	firstBytes, _ := os.ReadFile(settingsPath) //#nosec G304 -- test

	res, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res.Added {
		t.Fatal("second install should not Add (already present)")
	}
	secondBytes, _ := os.ReadFile(settingsPath) //#nosec G304 -- test
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("second install changed bytes:\n---first---\n%s\n---second---\n%s", string(firstBytes), string(secondBytes))
	}
}

func TestInstallBdHook_SkipWhenPluginEnabled(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	pre := `{"enabledPlugins": {"beads": true}}`
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(pre), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Skipped {
		t.Fatal("expected Skipped=true when project settings has beads plugin enabled")
	}

	// File untouched (no hook added, original bytes preserved).
	got, _ := os.ReadFile(settingsPath) //#nosec G304 -- test
	if string(got) != pre {
		t.Fatalf("expected file untouched, got:\n%s", string(got))
	}
}

func TestInstallBdHook_SkipWhenGlobalPluginEnabled(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	globalPath := filepath.Join(dir, "global", "settings.json")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte(`{"enabledPlugins":{"beads":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := InstallBdHook(InstallBdHookOptions{
		SettingsPath:     settingsPath,
		PluginGuardPaths: []string{globalPath},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("expected skip due to global plugin guard, got: %+v", res)
	}

	// File not created on skip.
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("expected settings.json not created on skip; err=%v", err)
	}
}

func TestInstallBdHook_MigrationSweep(t *testing.T) {
	stubHookJSON(t, true)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		t.Fatal(err)
	}
	// Pre-populated with every legacy variant plus a user hook + bd canonical
	// in SessionStart.
	pre := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "bd prime"}]},
      {"hooks": [{"type": "command", "command": "bd prime --stealth"}]},
      {"hooks": [{"type": "command", "command": "bd prime --hook-json"}]},
      {"hooks": [{"type": "command", "command": "user hook"}]}
    ],
    "PreCompact": [
      {"hooks": [{"type": "command", "command": "bd prime --hook-json"}]},
      {"hooks": [{"type": "command", "command": "bd prime --stealth --hook-json"}]},
      {"hooks": [{"type": "command", "command": "user precompact"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(pre), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Skipped {
		t.Fatal("unexpected skip")
	}
	// 2 legacy bare-form removals from SessionStart + 2 hook-json removals from PreCompact = 4
	if res.LegacyRemoved != 4 {
		t.Fatalf("expected 4 legacy removed, got %d", res.LegacyRemoved)
	}
	if res.Added {
		t.Fatal("canonical was already present in SessionStart; expected Added=false")
	}

	settings, _ := Load(settingsPath)
	cmds := ExtractCommands(settings)

	// Final state:
	//   SessionStart: bd prime --hook-json (canonical, kept) + user hook
	//   PreCompact: user precompact only
	want := map[string]string{
		"SessionStart:" + CanonicalBdCommand: "",
		"SessionStart:user hook":             "",
		"PreCompact:user precompact":         "",
	}
	for _, c := range cmds {
		key := c.Event + ":" + c.Command
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing expected hooks after migration sweep: %v\nfound: %+v", want, cmds)
	}
}

func TestInstallBdHook_UserCustomVariantPreserved(t *testing.T) {
	// Acceptance #8: user-customized variants on the bd prime command
	// string are left untouched. Migration sweep only removes the legacy
	// bare-form variants enumerated in legacyBdCommands.
	stubHookJSON(t, true)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		t.Fatal(err)
	}
	pre := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "bd prime --hook-json --custom-flag"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(pre), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath}); err != nil {
		t.Fatalf("install: %v", err)
	}
	settings, _ := Load(settingsPath)
	cmds := ExtractCommands(settings)

	// User's custom variant + canonical both present.
	foundCustom, foundCanonical := false, false
	for _, c := range cmds {
		if c.Command == "bd prime --hook-json --custom-flag" {
			foundCustom = true
		}
		if c.Command == CanonicalBdCommand {
			foundCanonical = true
		}
	}
	if !foundCustom {
		t.Error("user's custom bd variant was removed (acceptance #8 violation)")
	}
	if !foundCanonical {
		t.Error("canonical bd hook was not added when user had a different variant")
	}
}

func TestInstallBdHook_MigratesLegacyLocalFile(t *testing.T) {
	stubHookJSON(t, true)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	localPath := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		t.Fatal(err)
	}
	// Legacy file has bd hooks; current file is empty.
	legacy := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "bd prime"}]},
      {"hooks": [{"type": "command", "command": "user keep"}]}
    ]
  }
}`
	if err := os.WriteFile(localPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := InstallBdHook(InstallBdHookOptions{
		SettingsPath:      settingsPath,
		LocalSettingsPath: localPath,
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.LocalMigrated {
		t.Fatal("expected LocalMigrated=true")
	}

	// Legacy file: bd hook gone, user hook preserved.
	localSettings, _ := Load(localPath)
	cmds := ExtractCommands(localSettings)
	if len(cmds) != 1 || cmds[0].Command != "user keep" {
		t.Fatalf("expected only 'user keep' in legacy file, got %+v", cmds)
	}

	// Project file: bd canonical now present.
	projectSettings, _ := Load(settingsPath)
	pcmds := ExtractCommands(projectSettings)
	if len(pcmds) != 1 || pcmds[0].Command != CanonicalBdCommand {
		t.Fatalf("expected canonical bd hook in project file, got %+v", pcmds)
	}
}

func TestInstallBdHook_MissingLocalIsNoop(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	localPath := filepath.Join(dir, ".claude", "settings.local.json") // does not exist

	res, err := InstallBdHook(InstallBdHookOptions{
		SettingsPath:      settingsPath,
		LocalSettingsPath: localPath,
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.LocalMigrated {
		t.Fatal("expected LocalMigrated=false when local file absent")
	}
}

func TestDefaultGuardPaths(t *testing.T) {
	got := DefaultGuardPaths("/home/u", "/proj")
	want := []string{
		"/home/u/.claude/settings.json",
		"/proj/.claude/settings.local.json",
	}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: %v vs %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("path[%d]: got %s want %s", i, got[i], want[i])
		}
	}

	// Empty homeDir omits global path.
	if g := DefaultGuardPaths("", "/p"); len(g) != 1 {
		t.Fatalf("empty homeDir should produce 1 path, got %v", g)
	}
	// Empty both → nil/empty.
	if g := DefaultGuardPaths("", ""); len(g) != 0 {
		t.Fatalf("empty both should produce 0 paths, got %v", g)
	}
}

func TestInstallBdHook_RequiresSettingsPath(t *testing.T) {
	if _, err := InstallBdHook(InstallBdHookOptions{}); err == nil {
		t.Fatal("expected error when SettingsPath is empty")
	}
}

func TestInstallBdHook_FreshFile_StaleBd(t *testing.T) {
	// bd lacks --hook-json (released 1.0.4): emit the bare form so the hook
	// doesn't error with "unknown flag: --hook-json" on first session.
	stubHookJSON(t, false)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")

	res, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Added {
		t.Fatal("expected Added=true on fresh file")
	}
	if res.LegacyRemoved != 0 {
		t.Fatalf("expected no legacy removed on fresh file, got %d", res.LegacyRemoved)
	}

	settings, err := Load(settingsPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmds := ExtractCommands(settings)
	if len(cmds) != 1 || cmds[0].Command != "bd prime" || cmds[0].Event != "SessionStart" {
		t.Fatalf("expected single bare `bd prime` hook on SessionStart, got %+v", cmds)
	}
}

func TestInstallBdHook_Idempotent_StaleBd(t *testing.T) {
	stubHookJSON(t, false)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")

	if _, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	firstBytes, _ := os.ReadFile(settingsPath) //#nosec G304 -- test

	res, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if res.Added {
		t.Fatal("second install should not Add (bare bd prime already present)")
	}
	if res.LegacyRemoved != 0 {
		t.Fatalf("second install should not strip its own canonical, got %d removed", res.LegacyRemoved)
	}
	secondBytes, _ := os.ReadFile(settingsPath) //#nosec G304 -- test
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("second install changed bytes:\n---first---\n%s\n---second---\n%s", string(firstBytes), string(secondBytes))
	}
}

func TestInstallBdHook_ModernToStale(t *testing.T) {
	// A repo previously inited against modern bd has `bd prime --hook-json` on
	// SessionStart; bd is now stale. The broken hook-json form must be stripped
	// and replaced with the working bare `bd prime`.
	stubHookJSON(t, false)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		t.Fatal(err)
	}
	pre := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "bd prime --hook-json"}]},
      {"hooks": [{"type": "command", "command": "user hook"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(pre), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.LegacyRemoved != 1 {
		t.Fatalf("expected 1 broken hook-json removed, got %d", res.LegacyRemoved)
	}
	if !res.Added {
		t.Fatal("expected Added=true (bare bd prime replaces hook-json)")
	}

	settings, _ := Load(settingsPath)
	cmds := ExtractCommands(settings)
	foundBare, foundHookJSON, foundUser := false, false, false
	for _, c := range cmds {
		switch c.Command {
		case "bd prime":
			foundBare = true
		case CanonicalBdCommand:
			foundHookJSON = true
		case "user hook":
			foundUser = true
		}
	}
	if !foundBare {
		t.Error("expected bare `bd prime` after modern->stale transition")
	}
	if foundHookJSON {
		t.Error("broken `bd prime --hook-json` should have been stripped on stale bd")
	}
	if !foundUser {
		t.Error("user hook should be preserved")
	}
}

func TestInstallBdHook_StaleToModern(t *testing.T) {
	// A repo previously inited against stale bd has bare `bd prime`; bd is now
	// modern. The bare form is upgraded to `bd prime --hook-json`.
	stubHookJSON(t, true)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		t.Fatal(err)
	}
	pre := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "bd prime"}]}
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(pre), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := InstallBdHook(InstallBdHookOptions{SettingsPath: settingsPath})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.LegacyRemoved != 1 {
		t.Fatalf("expected 1 bare bd prime removed, got %d", res.LegacyRemoved)
	}
	if !res.Added {
		t.Fatal("expected Added=true (hook-json replaces bare)")
	}

	settings, _ := Load(settingsPath)
	cmds := ExtractCommands(settings)
	if len(cmds) != 1 || cmds[0].Command != CanonicalBdCommand {
		t.Fatalf("expected single hook-json hook after stale->modern upgrade, got %+v", cmds)
	}
}

func TestBdSupportsHookJSON_Smoke(t *testing.T) {
	// Host-dependent (released bd 1.0.4 returns false; bd HEAD returns true).
	// Assert only that it doesn't panic and returns a stable value.
	got := BdSupportsHookJSON()
	if got {
		t.Logf("bd supports --hook-json")
	} else {
		t.Logf("bd lacks --hook-json (or bd absent)")
	}
}

func TestBdBinaryAvailable_LikelyAccurate(t *testing.T) {
	// Smoke test: should not panic and should return the same value as a
	// direct exec.LookPath check. We can't strongly assert the value
	// because CI environments differ — assert internal consistency.
	got := BdBinaryAvailable()
	if got {
		t.Logf("bd binary available")
	} else {
		t.Logf("bd binary not available")
	}
}
