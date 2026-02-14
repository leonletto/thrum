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
		{".claude", "claude"},
		{".codex", "codex"},
		{".cursorrules", "cursor"},
		{".augment", "auggie"},
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
		{"AUGMENT_AGENT", "auggie"},
	}
	for _, c := range envChecks {
		if os.Getenv(c.envVar) != "" {
			return c.name
		}
	}

	return "cli-only"
}

// DetectedRuntime describes a runtime found during detection, including how it was found.
type DetectedRuntime struct {
	Name   string // e.g. "claude", "auggie"
	Source string // e.g. "found .claude/settings.json", "env CLAUDE_SESSION_ID"
}

// DetectAllRuntimes returns all detected runtimes (file markers + env vars).
// Results are deduplicated by name, with file-based detection taking priority.
func DetectAllRuntimes(repoPath string) []DetectedRuntime {
	seen := make(map[string]bool)
	var results []DetectedRuntime

	// Check file markers first (more reliable than env vars)
	fileChecks := []struct {
		marker string
		name   string
	}{
		{".claude/settings.json", "claude"},
		{".claude", "claude"},
		{".codex", "codex"},
		{".cursorrules", "cursor"},
		{".augment", "auggie"},
	}
	for _, c := range fileChecks {
		if seen[c.name] {
			continue
		}
		path := filepath.Join(repoPath, c.marker)
		if _, err := os.Stat(path); err == nil {
			results = append(results, DetectedRuntime{
				Name:   c.name,
				Source: "found " + c.marker,
			})
			seen[c.name] = true
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
		{"AUGMENT_AGENT", "auggie"},
	}
	for _, c := range envChecks {
		if seen[c.name] {
			continue
		}
		if os.Getenv(c.envVar) != "" {
			results = append(results, DetectedRuntime{
				Name:   c.name,
				Source: "env " + c.envVar,
			})
			seen[c.name] = true
		}
	}

	return results
}

// SupportedRuntimes returns the list of all supported runtime names.
func SupportedRuntimes() []string {
	return []string{"claude", "codex", "cursor", "gemini", "auggie", "cli-only", "all"}
}

// IsValidRuntime checks whether the given runtime name is valid.
func IsValidRuntime(name string) bool {
	return slices.Contains(SupportedRuntimes(), name)
}
