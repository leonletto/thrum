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
			name:     "CLI-only fallback",
			setup:    func(dir string) {},
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
	tests := []struct {
		name  string
		valid bool
	}{
		{"claude", true},
		{"codex", true},
		{"cursor", true},
		{"gemini", true},
		{"auggie", true},
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

func TestSupportedRuntimes(t *testing.T) {
	runtimes := SupportedRuntimes()
	if len(runtimes) < 6 {
		t.Errorf("expected at least 6 supported runtimes, got %d", len(runtimes))
	}

	// Verify required runtimes are present
	required := []string{"claude", "codex", "cursor", "gemini", "auggie", "cli-only"}
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
