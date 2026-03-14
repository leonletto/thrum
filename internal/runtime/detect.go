package runtime

import (
	"os"
	"path/filepath"
	"slices"
)

// DetectedAgent describes an agent found during detection.
type DetectedAgent struct {
	Name   string `json:"name"`
	Tier   string `json:"tier"`   // "repo", "env", or "binary"
	Source string `json:"source"` // what triggered the detection
}

// DetectAgents runs 3-tier detection and returns all found agents,
// deduplicated by name with the highest-tier match winning.
func DetectAgents(repoPath string) []DetectedAgent {
	return detectAgents(repoPath, true)
}

// detectAgents is the internal detection engine.
// When includeBinary is true, tier-3 binary scanning is performed.
func detectAgents(repoPath string, includeBinary bool) []DetectedAgent {
	seen := make(map[string]bool)
	var results []DetectedAgent

	agents := BuiltinAgents()

	// Tier 1: Repo markers
	for _, agent := range agents {
		for _, marker := range agent.RepoMarkers {
			if seen[agent.Name] {
				break
			}
			path := filepath.Join(repoPath, marker)
			if _, err := os.Stat(path); err == nil {
				results = append(results, DetectedAgent{
					Name:   agent.Name,
					Tier:   "repo",
					Source: "found " + marker,
				})
				seen[agent.Name] = true
				break
			}
		}
	}

	// Tier 2: Environment variables
	for _, agent := range agents {
		if seen[agent.Name] {
			continue
		}
		for _, envVar := range agent.EnvVars {
			if os.Getenv(envVar) != "" {
				results = append(results, DetectedAgent{
					Name:   agent.Name,
					Tier:   "env",
					Source: "env " + envVar,
				})
				seen[agent.Name] = true
				break
			}
		}
	}

	// Tier 3: Binary scan with verification
	if includeBinary {
		for _, agent := range agents {
			if seen[agent.Name] {
				continue
			}
			for _, bin := range agent.Binaries {
				if verifyBinary(bin) {
					results = append(results, DetectedAgent{
						Name:   agent.Name,
						Tier:   "binary",
						Source: "binary " + bin.Name,
					})
					seen[agent.Name] = true
					break
				}
			}
		}
	}

	return results
}

// DetectRuntime identifies the active AI coding runtime using all 3 detection
// tiers (repo markers, env vars, verified binary scan).
// Returns "cli-only" if no runtime is detected.
func DetectRuntime(repoPath string) string {
	results := detectAgents(repoPath, true)
	if len(results) > 0 {
		return results[0].Name
	}
	return "cli-only"
}

// DetectedRuntime describes a runtime found during detection, including how it was found.
type DetectedRuntime struct {
	Name   string // e.g. "claude", "auggie"
	Source string // e.g. "found .claude/settings.json", "env CLAUDE_SESSION_ID"
}

// DetectAllRuntimes returns all detected runtimes using all 3 detection tiers.
func DetectAllRuntimes(repoPath string) []DetectedRuntime {
	agents := detectAgents(repoPath, true)
	var results []DetectedRuntime
	for _, a := range agents {
		results = append(results, DetectedRuntime{
			Name:   a.Name,
			Source: a.Source,
		})
	}
	return results
}

// SupportedRuntimes returns the list of all supported runtime names,
// derived from the agent registry plus "cli-only" and "all" meta-values.
func SupportedRuntimes() []string {
	agents := BuiltinAgents()
	names := make([]string, 0, len(agents)+2)
	for _, a := range agents {
		names = append(names, a.Name)
	}
	names = append(names, "cli-only", "all")
	return names
}

// IsValidRuntime checks whether the given runtime name is valid.
func IsValidRuntime(name string) bool {
	return slices.Contains(SupportedRuntimes(), name)
}
