package runtime

import (
	"os"
	"path/filepath"
	"slices"
)

// DetectRuntime identifies the active AI coding runtime by checking file markers
// and environment variables. Returns "cli-only" if no runtime is detected.
func DetectRuntime(repoPath string) string {
	// Check file markers first (more reliable than env vars)
	fileChecks := []struct {
		marker string
		name   string
	}{
		{".claude/settings.json", "claude"},
		{".codex", "codex"},
		{".cursorrules", "cursor"},
	}
	for _, c := range fileChecks {
		path := filepath.Join(repoPath, c.marker)
		if _, err := os.Stat(path); err == nil {
			return c.name
		}
	}

	// Check environment variables
	envChecks := []struct {
		envVar string
		name   string
	}{
		{"CLAUDE_SESSION_ID", "claude"},
		{"CURSOR_SESSION", "cursor"},
		{"GEMINI_CLI", "gemini"},
	}
	for _, c := range envChecks {
		if os.Getenv(c.envVar) != "" {
			return c.name
		}
	}

	return "cli-only"
}

// SupportedRuntimes returns the list of all supported runtime names.
func SupportedRuntimes() []string {
	return []string{"claude", "codex", "cursor", "gemini", "cli-only", "all"}
}

// IsValidRuntime checks whether the given runtime name is valid.
func IsValidRuntime(name string) bool {
	return slices.Contains(SupportedRuntimes(), name)
}
