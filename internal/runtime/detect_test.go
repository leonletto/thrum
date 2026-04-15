package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectRuntime(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(tmpDir string)
		env      map[string]string
		expected string
	}{
		{
			name: "Claude via file marker",
			setup: func(dir string) {
				_ = os.MkdirAll(filepath.Join(dir, ".claude"), 0750)
				_ = os.WriteFile(filepath.Join(dir, ".claude/settings.json"), []byte("{}"), 0600)
			},
			expected: "claude",
		},
		{
			name: "Codex via directory",
			setup: func(dir string) {
				_ = os.MkdirAll(filepath.Join(dir, ".codex"), 0750)
			},
			expected: "codex",
		},
		{
			name: "Cursor via file marker",
			setup: func(dir string) {
				_ = os.WriteFile(filepath.Join(dir, ".cursorrules"), []byte("test"), 0600)
			},
			expected: "cursor",
		},
		{
			name: "Claude via env var",
			env: map[string]string{
				"CLAUDE_SESSION_ID": "test_session",
			},
			expected: "claude",
		},
		{
			name: "Cursor via env var",
			env: map[string]string{
				"CURSOR_SESSION": "test_session",
			},
			expected: "cursor",
		},
		{
			name: "Gemini via env var",
			env: map[string]string{
				"GEMINI_CLI": "true",
			},
			expected: "gemini",
		},
		{
			name: "Auggie via directory marker",
			setup: func(dir string) {
				_ = os.MkdirAll(filepath.Join(dir, ".augment"), 0750)
			},
			expected: "auggie",
		},
		{
			name: "Auggie via env var",
			env: map[string]string{
				"AUGMENT_AGENT": "1",
			},
			expected: "auggie",
		},
		{
			name:  "CLI-only fallback",
			setup: func(dir string) {},
			env: map[string]string{
				"PATH": "", // clear PATH so tier-3 binary scan finds nothing
			},
			expected: "cli-only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if tt.setup != nil {
				tt.setup(tmpDir)
			}

			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			result := DetectRuntime(tmpDir)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestDetectRuntime_FileMarkerPrecedence(t *testing.T) {
	// File markers should take precedence over env vars
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".codex"), 0750)
	t.Setenv("CLAUDE_SESSION_ID", "test_session")

	result := DetectRuntime(tmpDir)
	if result != "codex" {
		t.Errorf("file marker should take precedence over env var, got %q", result)
	}
}

func TestIsValidRuntime(t *testing.T) {
	// Isolate from any ~/.thrum/runtimes.json on the dev host so the
	// user-preset branch of SupportedRuntimes doesn't accidentally
	// match a test name.
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name  string
		valid bool
	}{
		{"claude", true},
		{"codex", true},
		{"cursor", true},
		{"gemini", true},
		{"auggie", true},
		{"kiro-cli", true},
		{"shell", true},
		{"cli-only", true},
		{"all", true},
		{"nonexistent", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidRuntime(tt.name); got != tt.valid {
				t.Errorf("IsValidRuntime(%q) = %v, want %v", tt.name, got, tt.valid)
			}
		})
	}
}

func TestIsValidRuntime_UserPreset(t *testing.T) {
	// Verify SupportedRuntimes picks up a preset defined in ~/.thrum/runtimes.json,
	// which is the path that failed in practice for 'thrum quickstart --runtime kiro-cli'
	// before kiro-cli became a builtin (thrum-e994).
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	configDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(configDir, 0750); err != nil {
		t.Fatal(err)
	}
	cfg := []byte(`{
  "custom_runtimes": {
    "my-custom-agent": {
      "name": "my-custom-agent",
      "display_name": "My Custom Agent",
      "command": "my-agent"
    }
  }
}`)
	if err := os.WriteFile(filepath.Join(configDir, "runtimes.json"), cfg, 0600); err != nil {
		t.Fatal(err)
	}

	if !IsValidRuntime("my-custom-agent") {
		t.Error("expected user-preset runtime to be valid; SupportedRuntimes does not merge user presets")
	}
}

func TestDetectAllRuntimes_MultipleDetected(t *testing.T) {
	t.Setenv("PATH", "") // isolate from system binaries
	tmpDir := t.TempDir()
	// Create Claude and Augment markers
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)
	_ = os.WriteFile(filepath.Join(tmpDir, ".claude/settings.json"), []byte("{}"), 0600)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".augment"), 0750)

	results := DetectAllRuntimes(tmpDir)
	if len(results) != 2 {
		t.Fatalf("expected 2 detected runtimes, got %d: %+v", len(results), results)
	}
	if results[0].Name != "claude" {
		t.Errorf("expected first result to be claude, got %q", results[0].Name)
	}
	if results[1].Name != "auggie" {
		t.Errorf("expected second result to be auggie, got %q", results[1].Name)
	}
	// Sources should reference file markers
	if results[0].Source != "found .claude/settings.json" {
		t.Errorf("expected source 'found .claude/settings.json', got %q", results[0].Source)
	}
}

func TestDetectAllRuntimes_NoneDetected(t *testing.T) {
	t.Setenv("PATH", "") // isolate from system binaries
	tmpDir := t.TempDir()
	results := DetectAllRuntimes(tmpDir)
	if len(results) != 0 {
		t.Errorf("expected 0 detected runtimes, got %d: %+v", len(results), results)
	}
}

func TestDetectAllRuntimes_DeduplicatesFileAndEnv(t *testing.T) {
	tmpDir := t.TempDir()
	// Claude via file marker AND env var — should only appear once
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)
	_ = os.WriteFile(filepath.Join(tmpDir, ".claude/settings.json"), []byte("{}"), 0600)
	t.Setenv("CLAUDE_SESSION_ID", "test_session")

	results := DetectAllRuntimes(tmpDir)
	claudeCount := 0
	for _, r := range results {
		if r.Name == "claude" {
			claudeCount++
		}
	}
	if claudeCount != 1 {
		t.Errorf("expected claude to appear once, got %d times", claudeCount)
	}
	// File marker should win (first in results)
	if results[0].Source != "found .claude/settings.json" {
		t.Errorf("expected file marker source, got %q", results[0].Source)
	}
}

func TestDetectAllRuntimes_EnvOnly(t *testing.T) {
	t.Setenv("PATH", "") // isolate from system binaries
	tmpDir := t.TempDir()
	t.Setenv("GEMINI_CLI", "true")

	results := DetectAllRuntimes(tmpDir)
	if len(results) != 1 {
		t.Fatalf("expected 1 detected runtime, got %d: %+v", len(results), results)
	}
	if results[0].Name != "gemini" {
		t.Errorf("expected gemini, got %q", results[0].Name)
	}
	if results[0].Source != "env GEMINI_CLI" {
		t.Errorf("expected source 'env GEMINI_CLI', got %q", results[0].Source)
	}
}

func TestDetectAgents_Tier1_RepoMarkers(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)
	_ = os.WriteFile(filepath.Join(tmpDir, ".claude/settings.json"), []byte("{}"), 0600)

	results := DetectAgents(tmpDir)
	if len(results) == 0 {
		t.Fatal("expected at least one detected agent")
	}
	if results[0].Name != "claude" {
		t.Errorf("expected claude, got %q", results[0].Name)
	}
	if results[0].Tier != "repo" {
		t.Errorf("expected tier 'repo', got %q", results[0].Tier)
	}
}

func TestDetectAgents_Tier2_EnvVars(t *testing.T) {
	tmpDir := t.TempDir() // no repo markers
	t.Setenv("GEMINI_CLI", "true")

	results := DetectAgents(tmpDir)
	found := false
	for _, r := range results {
		if r.Name == "gemini" && r.Tier == "env" {
			found = true
		}
	}
	if !found {
		t.Error("expected gemini detected via env var")
	}
}

func TestDetectAgents_DeduplicatesByName(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)
	_ = os.WriteFile(filepath.Join(tmpDir, ".claude/settings.json"), []byte("{}"), 0600)
	t.Setenv("CLAUDE_SESSION_ID", "test")

	results := DetectAgents(tmpDir)
	claudeCount := 0
	for _, r := range results {
		if r.Name == "claude" {
			claudeCount++
		}
	}
	if claudeCount != 1 {
		t.Errorf("expected claude once, got %d times", claudeCount)
	}
	// Repo tier should win
	for _, r := range results {
		if r.Name == "claude" && r.Tier != "repo" {
			t.Errorf("expected repo tier to win, got %q", r.Tier)
		}
	}
}

func TestDetectAgents_MultipleAgents(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".cursor"), 0750)

	results := DetectAgents(tmpDir)
	names := make(map[string]bool)
	for _, r := range results {
		names[r.Name] = true
	}
	if !names["claude"] || !names["cursor"] {
		t.Errorf("expected both claude and cursor detected, got %v", names)
	}
}

func TestSupportedRuntimes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	runtimes := SupportedRuntimes()
	if len(runtimes) < 6 {
		t.Errorf("expected at least 6 supported runtimes, got %d", len(runtimes))
	}

	// Verify required runtimes are present. kiro-cli and shell were added
	// in thrum-e994 when the SupportedRuntimes source expanded from
	// builtinAgents to BuiltinPresets ∪ builtinAgents ∪ user presets.
	required := []string{"claude", "codex", "cursor", "gemini", "auggie", "kiro-cli", "shell", "cli-only"}
	for _, name := range required {
		found := false
		for _, r := range runtimes {
			if r == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing required runtime: %s", name)
		}
	}
}
