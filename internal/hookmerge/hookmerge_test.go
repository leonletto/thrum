package hookmerge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoad_MissingFile(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load on empty file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json {{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	in := Settings{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo hi"},
					},
				},
			},
		},
		"model": "claude-sonnet",
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch:\n in=%v\nout=%v", in, out)
	}

	// File ends with a trailing newline so editors don't fight it.
	raw, _ := os.ReadFile(path) //#nosec G304 -- test fixture
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatal("expected trailing newline in saved file")
	}

	// Indentation is 2 spaces (matches bd setup claude format).
	var indented map[string]any
	if err := json.Unmarshal(raw, &indented); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
}

func TestAddHookCommand_NewEvent(t *testing.T) {
	settings := Settings{}
	added := AddHookCommand(settings, "SessionStart", "thrum cmd")
	if !added {
		t.Fatal("expected add to return true")
	}
	cmds := ExtractCommands(settings)
	if len(cmds) != 1 || cmds[0].Event != "SessionStart" || cmds[0].Command != "thrum cmd" {
		t.Fatalf("unexpected extracted commands: %v", cmds)
	}
}

func TestAddHookCommand_Idempotent(t *testing.T) {
	settings := Settings{}
	AddHookCommand(settings, "SessionStart", "x")
	added := AddHookCommand(settings, "SessionStart", "x")
	if added {
		t.Fatal("expected second add to return false")
	}
	cmds := ExtractCommands(settings)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command after dedup, got %d: %v", len(cmds), cmds)
	}
}

func TestAddHookCommand_PreservesThirdParty(t *testing.T) {
	settings := Settings{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "bd prime --hook-json"},
					},
				},
			},
		},
		"model": "claude-sonnet",
	}
	added := AddHookCommand(settings, "SessionStart", "thrum cmd")
	if !added {
		t.Fatal("expected add to return true")
	}
	cmds := ExtractCommands(settings)
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d: %v", len(cmds), cmds)
	}
	// bd's entry must survive.
	foundBd := false
	for _, c := range cmds {
		if c.Command == "bd prime --hook-json" {
			foundBd = true
		}
	}
	if !foundBd {
		t.Fatal("third-party bd hook was dropped")
	}
	if settings["model"] != "claude-sonnet" {
		t.Fatal("unrelated top-level field was modified")
	}
}

func TestRemoveHookCommand_Exact(t *testing.T) {
	settings := Settings{}
	AddHookCommand(settings, "SessionStart", "keep")
	AddHookCommand(settings, "SessionStart", "drop")
	removed := RemoveHookCommand(settings, "SessionStart", "drop")
	if !removed {
		t.Fatal("expected remove to return true")
	}
	cmds := ExtractCommands(settings)
	if len(cmds) != 1 || cmds[0].Command != "keep" {
		t.Fatalf("unexpected remaining commands: %v", cmds)
	}
}

func TestRemoveHookCommand_LastEntryDropsEventKey(t *testing.T) {
	settings := Settings{}
	AddHookCommand(settings, "PreCompact", "bd prime --hook-json")
	if !RemoveHookCommand(settings, "PreCompact", "bd prime --hook-json") {
		t.Fatal("expected remove to return true")
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if ok {
		if _, hasEvent := hooks["PreCompact"]; hasEvent {
			t.Fatal("PreCompact key should be deleted after last hook removed")
		}
	}
}

func TestRemoveHookCommand_Absent(t *testing.T) {
	settings := Settings{}
	AddHookCommand(settings, "SessionStart", "x")
	if RemoveHookCommand(settings, "SessionStart", "not-present") {
		t.Fatal("expected remove of absent command to return false")
	}
	if RemoveHookCommand(settings, "NonExistentEvent", "x") {
		t.Fatal("expected remove on missing event to return false")
	}
}

func TestRemoveHookCommand_DroppingOneCommandPreservesSiblings(t *testing.T) {
	// Bd's installer can place multiple commands in a single entry's hooks
	// array. Removing one must not drop sibling commands in the same entry.
	settings := Settings{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "bd prime"},
						map[string]any{"type": "command", "command": "user hook"},
					},
				},
			},
		},
	}
	if !RemoveHookCommand(settings, "SessionStart", "bd prime") {
		t.Fatal("expected remove to return true")
	}
	cmds := ExtractCommands(settings)
	if len(cmds) != 1 || cmds[0].Command != "user hook" {
		t.Fatalf("expected sibling 'user hook' preserved, got %v", cmds)
	}
}

func TestHasBeadsPlugin(t *testing.T) {
	cases := []struct {
		name string
		in   Settings
		want bool
	}{
		{name: "no enabledPlugins", in: Settings{}, want: false},
		{name: "wrong shape", in: Settings{"enabledPlugins": "string"}, want: false},
		{name: "beads false", in: Settings{"enabledPlugins": map[string]any{"beads": false}}, want: false},
		{name: "beads true exact", in: Settings{"enabledPlugins": map[string]any{"beads": true}}, want: true},
		{name: "beads mixed case", in: Settings{"enabledPlugins": map[string]any{"Beads": true}}, want: true},
		{name: "beads in compound key", in: Settings{"enabledPlugins": map[string]any{"my-beads-plugin": true}}, want: true},
		{name: "non-beads key true", in: Settings{"enabledPlugins": map[string]any{"other": true}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HasBeadsPlugin(tc.in)
			if got != tc.want {
				t.Fatalf("HasBeadsPlugin(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractCommands_StableEventOrder(t *testing.T) {
	settings := Settings{}
	// Insert in non-alphabetical order; expect alphabetical out.
	AddHookCommand(settings, "Stop", "a")
	AddHookCommand(settings, "PreCompact", "b")
	AddHookCommand(settings, "SessionStart", "c")
	cmds := ExtractCommands(settings)
	if len(cmds) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(cmds))
	}
	if cmds[0].Event != "PreCompact" || cmds[1].Event != "SessionStart" || cmds[2].Event != "Stop" {
		t.Fatalf("expected alphabetical event order, got %+v", cmds)
	}
}
