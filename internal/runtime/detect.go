package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
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
// merging three sources: built-in presets (presets.go), built-in agents
// (registry.go, for detection-only entries), and user-defined presets
// loaded from ~/.thrum/runtimes.json. The meta-values "cli-only" and
// "all" are always appended. The result is de-duplicated and sorted.
func SupportedRuntimes() []string {
	seen := make(map[string]bool)
	var names []string

	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}

	// All built-in presets (includes "shell" which is absent from builtinAgents).
	for name := range BuiltinPresets {
		add(name)
	}

	// All built-in agents (mostly overlaps with presets, but acts as a safety net
	// if a detection-only entry is ever added without a preset).
	for _, a := range BuiltinAgents() {
		add(a.Name)
	}

	// User-defined presets from ~/.thrum/runtimes.json. Ignore load errors —
	// absence or parse failure should not block validation of built-ins.
	if userPresets, err := loadUserPresets(); err == nil {
		for name := range userPresets {
			add(name)
		}
	}

	// Meta-values accepted by init/skills commands.
	add("cli-only")
	add("all")

	sort.Strings(names)
	return names
}

// IsValidRuntime checks whether the given runtime name is valid.
func IsValidRuntime(name string) bool {
	return slices.Contains(SupportedRuntimes(), name)
}
