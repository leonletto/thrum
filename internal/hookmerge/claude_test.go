package hookmerge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const thrumTemplate = `{
  "hooks": {
    "SessionStart": [{
      "hooks": [{
        "type": "command",
        "command": "bash \"${CLAUDE_PROJECT_DIR}/scripts/thrum-startup.sh\""
      }]
    }],
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "HOOK_EVENT=Stop bash \"${CLAUDE_PROJECT_DIR}/scripts/thrum-check-inbox.sh\""
      }]
    }]
  }
}
`

func TestMergeClaudeSettings_CreateWhenAbsent(t *testing.T) {
	out := filepath.Join(t.TempDir(), "settings.json")
	res, err := MergeClaudeSettings(out, thrumTemplate, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Action != "create" {
		t.Fatalf("expected action=create, got %s", res.Action)
	}
	// File is normalized through Save (not verbatim) so subsequent merges
	// produce byte-stable output. Check semantic equality, not bytes.
	got, err := Load(out)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmds := ExtractCommands(got)
	if len(cmds) != 2 {
		t.Fatalf("expected 2 thrum commands after create, got %d: %+v", len(cmds), cmds)
	}
}

func TestMergeClaudeSettings_ForceOverwrite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "settings.json")
	// Pre-existing arbitrary content the user wrote.
	if err := os.WriteFile(out, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"user only"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := MergeClaudeSettings(out, thrumTemplate, true)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Action != "overwrite" {
		t.Fatalf("expected overwrite, got %s", res.Action)
	}
	// User content is gone; only thrum hooks remain.
	got, _ := Load(out)
	cmds := ExtractCommands(got)
	for _, c := range cmds {
		if c.Command == "user only" {
			t.Fatal("user content survived force-overwrite (should have been replaced)")
		}
	}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 thrum commands, got %d", len(cmds))
	}
}

func TestMergeClaudeSettings_MergeAddsMissingHooks(t *testing.T) {
	out := filepath.Join(t.TempDir(), "settings.json")
	// File exists with only a user hook and bd's hook in SessionStart.
	pre := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "bd prime --hook-json"}]},
      {"hooks": [{"type": "command", "command": "user custom"}]}
    ]
  },
  "model": "claude-sonnet-4-5"
}
`
	if err := os.WriteFile(out, []byte(pre), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := MergeClaudeSettings(out, thrumTemplate, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Action != "merge" {
		t.Fatalf("expected merge, got %s", res.Action)
	}

	// Reload and inspect.
	merged, err := Load(out)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmds := ExtractCommands(merged)

	// bd, user, and thrum's SessionStart hook all present.
	want := map[string]bool{
		"bd prime --hook-json": false,
		"user custom":          false,
		`bash "${CLAUDE_PROJECT_DIR}/scripts/thrum-startup.sh"`:                     false,
		`HOOK_EVENT=Stop bash "${CLAUDE_PROJECT_DIR}/scripts/thrum-check-inbox.sh"`: false,
	}
	for _, c := range cmds {
		if _, ok := want[c.Command]; ok {
			want[c.Command] = true
		}
	}
	for k, v := range want {
		if !v {
			t.Errorf("expected command present after merge: %q", k)
		}
	}

	// Unrelated top-level field preserved.
	if merged["model"] != "claude-sonnet-4-5" {
		t.Errorf("model field dropped during merge: %v", merged["model"])
	}
}

func TestMergeClaudeSettings_Idempotent(t *testing.T) {
	out := filepath.Join(t.TempDir(), "settings.json")
	// Run merge twice; second run must report noop.
	if _, err := MergeClaudeSettings(out, thrumTemplate, false); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	// Save the bytes + mtime after first merge so we can compare.
	firstBytes, _ := os.ReadFile(out) //#nosec G304 -- test
	firstInfo, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	firstMtime := firstInfo.ModTime()

	// Wait long enough that any second write would tick the filesystem
	// mtime resolution (most OS file mtime granularity is 1s).
	time.Sleep(1100 * time.Millisecond)

	res, err := MergeClaudeSettings(out, thrumTemplate, false)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if res.Action != "noop" || !res.AlreadyCurrent {
		t.Fatalf("expected noop+AlreadyCurrent, got %+v", res)
	}
	// Confirm: second run produces byte-identical output to first.
	secondBytes, _ := os.ReadFile(out) //#nosec G304
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("second merge changed bytes:\n--- first ---\n%s\n--- second ---\n%s", string(firstBytes), string(secondBytes))
	}
	// Acceptance #3: idempotent re-run must not bump mtime. The merge
	// path skips Save when the file already carries every thrum hook;
	// IDEs and git-status-by-mtime tooling treat an mtime bump as a
	// dirty file.
	secondInfo, _ := os.Stat(out)
	if !secondInfo.ModTime().Equal(firstMtime) {
		t.Errorf("noop merge bumped mtime: first=%v second=%v", firstMtime, secondInfo.ModTime())
	}
}

// TestPreviewClaudeMerge_NoopWhenCurrent verifies that dry-run reports
// "noop" (not "merge") when the on-disk file already carries every thrum
// hook. The previous stat-only dry-run path misreported "merge" for
// current files (review finding BLOCKING #2).
func TestPreviewClaudeMerge_NoopWhenCurrent(t *testing.T) {
	out := filepath.Join(t.TempDir(), "settings.json")
	// Seed file by running real merge once.
	if _, err := MergeClaudeSettings(out, thrumTemplate, false); err != nil {
		t.Fatalf("seed merge: %v", err)
	}
	res, err := PreviewClaudeMerge(out, thrumTemplate, false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Action != "noop" {
		t.Fatalf("expected preview action=noop for current file, got %q", res.Action)
	}
}

func TestPreviewClaudeMerge_DoesNotWrite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "settings.json")
	// File absent → preview reports "create" but must NOT create the file.
	res, err := PreviewClaudeMerge(out, thrumTemplate, false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Action != "create" {
		t.Fatalf("expected create, got %s", res.Action)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("preview created file; should be stat-only — err=%v", err)
	}
}

func TestMergeClaudeSettings_MalformedExistingErrors(t *testing.T) {
	out := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(out, []byte("not json {{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeClaudeSettings(out, thrumTemplate, false); err == nil {
		t.Fatal("expected error on malformed existing settings")
	}
}

func TestMergeClaudeSettings_MalformedRenderedErrors(t *testing.T) {
	out := filepath.Join(t.TempDir(), "settings.json")
	// File exists so we hit the merge path (not the verbatim write path).
	if err := os.WriteFile(out, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeClaudeSettings(out, "not json {{", false); err == nil {
		t.Fatal("expected error on malformed rendered template")
	}
}

// Compile-time check that the rendered template parses as JSON — guards
// against typos in the canonical template path.
func TestThrumTemplate_IsValidJSON(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(thrumTemplate), &m); err != nil {
		t.Fatalf("thrum template is invalid JSON: %v", err)
	}
}
