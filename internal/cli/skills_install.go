package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/runtime"
)

// SkillsInstallOptions contains options for skills-only installation.
type SkillsInstallOptions struct {
	RepoPath string
	Agent    string // agent name (e.g., "claude", "cursor")
	Force    bool
	DryRun   bool
}

// SkillsInstallResult contains the result of a skills installation.
type SkillsInstallResult struct {
	Agent       string   `json:"agent"`
	InstallPath string   `json:"install_path"`
	Files       []string `json:"files"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

// InstallSkills copies embedded thrum skill files to the appropriate
// skills directory for the given agent.
func InstallSkills(opts SkillsInstallOptions) (*SkillsInstallResult, error) {
	agent, ok := runtime.GetAgent(opts.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", opts.Agent)
	}

	installDir := resolveSkillsPath(opts.RepoPath, agent)
	absPath := filepath.Join(opts.RepoPath, installDir)

	// Check for existing installation
	if !opts.Force {
		if _, err := os.Stat(filepath.Join(absPath, "SKILL.md")); err == nil {
			return nil, fmt.Errorf("skill already installed at %s (use --force to overwrite)", installDir)
		}
	}

	result := &SkillsInstallResult{
		Agent:       opts.Agent,
		InstallPath: installDir,
		DryRun:      opts.DryRun,
	}

	// Walk embedded skill files and copy them
	err := fs.WalkDir(SkillFS, "skill/thrum", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Strip the "skill/thrum" prefix to get relative path
		relPath := strings.TrimPrefix(path, "skill/thrum")
		if relPath == "" {
			return nil // root directory
		}
		relPath = relPath[1:] // remove leading "/"

		outPath := filepath.Join(absPath, relPath)

		if d.IsDir() {
			if !opts.DryRun {
				return os.MkdirAll(outPath, 0750)
			}
			return nil
		}

		result.Files = append(result.Files, relPath)

		if opts.DryRun {
			return nil
		}

		data, readErr := SkillFS.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read embedded %s: %w", path, readErr)
		}

		if mkErr := os.MkdirAll(filepath.Dir(outPath), 0750); mkErr != nil {
			return fmt.Errorf("mkdir for %s: %w", outPath, mkErr)
		}
		return os.WriteFile(outPath, data, 0644)
	})
	if err != nil {
		return nil, fmt.Errorf("install skills: %w", err)
	}

	return result, nil
}

// resolveSkillsPath determines the skills install directory.
// If the agent's config dir exists in the repo, use the agent's preferred
// skills path. Otherwise fall back to .agents/skills/thrum.
func resolveSkillsPath(repoPath string, agent runtime.AgentDefinition) string {
	for _, marker := range agent.RepoMarkers {
		markerPath := filepath.Join(repoPath, marker)
		info, err := os.Stat(markerPath)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return filepath.Join(agent.SkillsDir, "thrum")
		}
		// File marker — check if its parent dir qualifies
		parentDir := filepath.Dir(marker)
		if parentDir != "." {
			return filepath.Join(agent.SkillsDir, "thrum")
		}
	}

	// Fallback: generic cross-agent path
	return ".agents/skills/thrum"
}

// FormatSkillsInstall formats the result for human-readable display.
func FormatSkillsInstall(result *SkillsInstallResult) string {
	var out strings.Builder

	if result.DryRun {
		fmt.Fprintf(&out, "Dry run — would install thrum skill for %s:\n", result.Agent)
	} else {
		fmt.Fprintf(&out, "Skill installed to %s/\n", result.InstallPath)
	}
	for _, f := range result.Files {
		fmt.Fprintf(&out, "  %s\n", f)
	}
	return out.String()
}
