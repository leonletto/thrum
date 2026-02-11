package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/paths"
)

// Config represents the resolved configuration for the thrum agent.
type Config struct {
	RepoID  string      // Repository ID (from identity file or generated)
	Agent   AgentConfig // Agent identity configuration
	Display string      // Display name for the agent
}

// AgentConfig represents agent identity information.
type AgentConfig struct {
	Kind    string // "agent" or "human"
	Name    string // Agent name (e.g., "furiosa", or hash-based like "coordinator_1B9K")
	Role    string // Agent role (e.g., "implementer", "planner")
	Module  string // Module/component responsibility
	Display string // Display name
}

// IdentityFile represents the identity file structure stored in .thrum/identities/{name}.json.
type IdentityFile struct {
	Version     int         `json:"version"`
	RepoID      string      `json:"repo_id"`
	Agent       AgentConfig `json:"agent"`
	Worktree    string      `json:"worktree"` // Worktree name (e.g., "daemon", "foundation")
	ConfirmedBy string      `json:"confirmed_by"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Load loads configuration with the following priority:
// 1. THRUM_NAME env var to select which identity file (highest)
// 2. Environment variables (THRUM_ROLE, THRUM_MODULE, THRUM_DISPLAY)
// 3. CLI flags (passed as overrides)
// 4. Identity file in .thrum/identities/ directory
// 5. Returns error if required fields are missing.
func Load(flagRole, flagModule string) (*Config, error) {
	return LoadWithPath(".", flagRole, flagModule)
}

// LoadWithPath loads configuration from a specific repo path.
func LoadWithPath(repoPath, flagRole, flagModule string) (*Config, error) {
	cfg := &Config{}

	// Get THRUM_NAME env var for identity selection
	thrumName := os.Getenv("THRUM_NAME")

	// Try to load identity from identities directory (new format)
	identitiesDir := filepath.Join(repoPath, ".thrum", "identities")
	identityResult, err := loadIdentityFromDir(identitiesDir, thrumName)
	if err == nil {
		cfg.RepoID = identityResult.RepoID
		cfg.Agent = identityResult.Agent
		cfg.Display = identityResult.Agent.Display
	} else {
		// If THRUM_NAME is explicitly set, don't fallback - return the error
		if thrumName != "" {
			return nil, err
		}
		// If the error is about multiple identity files (not "no identity" or "read dir"),
		// propagate it — the user needs to resolve the ambiguity
		if strings.Contains(err.Error(), "cannot auto-select identity") {
			return nil, err
		}
		// If this is a redirected worktree, don't silently fall through —
		// the user must register an identity here, not inherit from the main repo
		if paths.IsRedirected(repoPath) {
			return nil, fmt.Errorf("no agent identities registered in this worktree\n  Register with: thrum quickstart --name <name> --role <role> --module <module>")
		}
		// No identity file found - will rely on env vars or CLI flags
	}

	// Environment variables override identity file
	if role := os.Getenv("THRUM_ROLE"); role != "" {
		cfg.Agent.Role = role
	}
	if module := os.Getenv("THRUM_MODULE"); module != "" {
		cfg.Agent.Module = module
	}
	if display := os.Getenv("THRUM_DISPLAY"); display != "" {
		cfg.Display = display
		cfg.Agent.Display = display
	}

	// CLI flags override everything
	if flagRole != "" {
		cfg.Agent.Role = flagRole
	}
	if flagModule != "" {
		cfg.Agent.Module = flagModule
	}

	// Validate required fields
	if cfg.Agent.Role == "" {
		return nil, fmt.Errorf("role not specified: set THRUM_ROLE, use --role flag, or create identity file")
	}
	if cfg.Agent.Module == "" {
		return nil, fmt.Errorf("module not specified: set THRUM_MODULE, use --module flag, or create identity file")
	}

	// Default kind to "agent" if not set
	if cfg.Agent.Kind == "" {
		cfg.Agent.Kind = "agent"
	}

	return cfg, nil
}

// loadIdentityFile loads and parses a single identity file.
func loadIdentityFile(path string) (*IdentityFile, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal identities directory
	if err != nil {
		return nil, fmt.Errorf("read identity file: %w", err)
	}

	var identity IdentityFile
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, fmt.Errorf("parse identity file: %w", err)
	}

	return &identity, nil
}

// loadIdentityFromDir scans the identities directory and returns the identity based on priority:
// 1. If thrumName is provided (from THRUM_NAME env var), load that specific identity file
// 2. If only one identity file exists, load it (solo-agent worktree backward compat)
// 3. Otherwise, error.
func loadIdentityFromDir(dirPath string, thrumName string) (*IdentityFile, error) {
	// If THRUM_NAME is specified, validate and load that specific identity file
	if thrumName != "" {
		if err := identity.ValidateAgentName(thrumName); err != nil {
			return nil, fmt.Errorf("invalid THRUM_NAME: %w", err)
		}

		identityPath := filepath.Join(dirPath, thrumName+".json")
		identityFile, err := loadIdentityFile(identityPath)
		if err != nil {
			return nil, fmt.Errorf("load identity file %s: %w", thrumName+".json", err)
		}
		return identityFile, nil
	}

	// Otherwise, scan directory for identity files
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("read identities directory: %w", err)
	}

	// Filter to .json files only
	var jsonFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			jsonFiles = append(jsonFiles, entry.Name())
		}
	}

	if len(jsonFiles) == 0 {
		return nil, fmt.Errorf("no identity files found")
	}

	// If exactly one identity file, load it (solo-agent worktree)
	if len(jsonFiles) == 1 {
		identityPath := filepath.Join(dirPath, jsonFiles[0])
		return loadIdentityFile(identityPath)
	}

	// Collect available identity names for error messages
	available := make([]string, 0, len(jsonFiles))
	for _, f := range jsonFiles {
		available = append(available, strings.TrimSuffix(f, ".json"))
	}

	// Multiple identity files - try worktree-based filtering
	currentWT := detectCurrentWorktree(dirPath)
	if currentWT != "" {
		var matches []*IdentityFile
		for _, f := range jsonFiles {
			id, err := loadIdentityFile(filepath.Join(dirPath, f))
			if err != nil {
				continue
			}
			if id.Worktree == currentWT {
				matches = append(matches, id)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			// Most-recent-wins: pick the identity with the latest UpdatedAt
			best := matches[0]
			for _, m := range matches[1:] {
				if m.UpdatedAt.After(best.UpdatedAt) {
					best = m
				}
			}
			return best, nil
		}
		// Zero matches: fall through to generic error
	}

	return nil, fmt.Errorf("cannot auto-select identity: %d identity files found in .thrum/identities/\n  Hint: set THRUM_NAME=<name> to select one, or run from the correct worktree\n  Available: %s",
		len(jsonFiles), strings.Join(available, ", "))
}

// detectCurrentWorktree returns the current git worktree name, or "" if detection fails.
func detectCurrentWorktree(identitiesDir string) string {
	// identitiesDir is .thrum/identities/ — go up two levels to get repo root
	repoRoot := filepath.Dir(filepath.Dir(identitiesDir))
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--show-toplevel") //nolint:gosec // G204 - args are constant strings, not user input
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSpace(string(out)))
}

// SaveIdentityFile writes an identity file to disk in the identities directory.
// The filename is derived from the agent name (e.g., "furiosa.json" or "coordinator_1B9K.json").
func SaveIdentityFile(thrumDir string, identity *IdentityFile) error {
	// Ensure identities directory exists
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		return fmt.Errorf("create identities directory: %w", err)
	}

	// Derive filename from agent name
	filename := identity.Agent.Name + ".json"
	if identity.Agent.Name == "" {
		// Fallback for legacy unnamed agents - use role_module.json
		filename = fmt.Sprintf("%s_%s.json", identity.Agent.Role, identity.Agent.Module)
	}

	identityPath := filepath.Join(identitiesDir, filename)

	identity.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal identity: %w", err)
	}

	if err := os.WriteFile(identityPath, data, 0600); err != nil {
		return fmt.Errorf("write identity file: %w", err)
	}

	return nil
}
