package permission

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/identity"
)

// ResolveSupervisorID returns the canonical per-repo per-owner supervisor
// agent ID: "supervisor_<repo-slug>_<user-slug>".
func ResolveSupervisorID(cfg *config.ThrumConfig, repoPath string) string {
	return "supervisor_" + resolveRepoSlug(cfg, repoPath) + "_" + resolveUserSlug(repoPath)
}

// ResolveLegacySupervisorID returns the pre-upgrade form of the supervisor
// ID, matching exactly what the old binary produced via
// ResolveProjectName. This is used by the receiver path for backward
// compat during the upgrade window.
//
// Old-binary fallback order: cfg.ProjectName → filepath.Base(repoPath) → "project".
// This function MUST match that order exactly.
func ResolveLegacySupervisorID(cfg *config.ThrumConfig, repoPath string) string {
	return "supervisor_" + ResolveProjectName(cfg, repoPath)
}

// ResolveProjectName returns a human-readable repo/project display name
// for use in nudge bodies and other user-facing UI. Resolution order:
// cfg.ProjectName → filepath.Base(repoPath) → "project".
//
// Exported (unlike the sibling resolveRepoSlug) because two independent
// external consumers need the same logic:
//
//  1. cmd/thrum/main.go's runDaemon builds the `projectName` variable it
//     hands to permission.New for FormatNudge's "Repo:" display line.
//  2. ResolveLegacySupervisorID derives the pre-upgrade supervisor form
//     (`supervisor_<project>`) that the receiver path accepts for
//     cross-version compat.
//
// Distinct from resolveRepoSlug (the supervisor-ID repo half): this
// function never returns an origin-URL hash, and the output is intended
// for display and legacy compat rather than for the new supervisor-ID
// embedding.
func ResolveProjectName(cfg *config.ThrumConfig, repoPath string) string {
	if cfg != nil && cfg.ProjectName != "" {
		return cfg.ProjectName
	}
	if repoPath != "" {
		base := filepath.Base(repoPath)
		if base != "" && base != "." && base != "/" {
			return base
		}
	}
	return "project"
}

// SupervisorIdentity returns the synthesized IdentityFile for the virtual
// supervisor pseudo-agent. Never written to disk; produced on demand.
func SupervisorIdentity(cfg *config.ThrumConfig, repoPath string) *config.IdentityFile {
	id := ResolveSupervisorID(cfg, repoPath)
	// Display uses the repo slug since that's the human-recognizable half.
	displayRepo := resolveRepoSlug(cfg, repoPath)
	return &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    id,
			Role:    "supervisor",
			Module:  "daemon",
			Display: "Supervisor (" + displayRepo + ")",
		},
		Reserved: true,
	}
}

// resolveRepoSlug selects the repo-identifying half of the supervisor ID.
// Order: cfg.ProjectName → origin-URL hash → filepath.Base.
func resolveRepoSlug(cfg *config.ThrumConfig, repoPath string) string {
	if cfg != nil && cfg.ProjectName != "" {
		return identity.SanitizeAgentName(cfg.ProjectName)
	}
	if hash := gitOriginHash(repoPath); hash != "" {
		return hash
	}
	return identity.SanitizeAgentName(filepath.Base(repoPath))
}

// gitOriginHash returns a 12-char lowercase Crockford-base32 hash of
// the NORMALIZED git origin URL, or "" if no origin is configured or
// the URL fails to parse. Reuses identity.GenerateRepoID for
// normalization so the hash is stable across SSH vs HTTPS variants of
// the same repo (identity.GenerateRepoID calls normalizeGitURL
// internally).
//
// The returned string is the 12-char hash portion only (the "r_"
// prefix from GenerateRepoID is stripped since this value is embedded
// in "supervisor_<...>" where "r_" would be meaningless noise).
func gitOriginHash(repoPath string) string {
	origin, err := safecmd.GitConfig(context.Background(), repoPath, "remote.origin.url")
	if err != nil || origin == "" {
		return ""
	}
	repoID, err := identity.GenerateRepoID(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(repoID, "r_"))
}

// resolveUserSlug selects the owner-identifying half of the supervisor ID.
// Order: git config user.name → $USER → hostname. Each branch applies
// BOTH guards:
//
//  1. Raw-empty guard — SanitizeAgentName("") returns "main", so skipping
//     the empty string up-front avoids the "main" sentinel leaking when
//     the source genuinely had no value.
//  2. Post-sanitize "main" guard — an all-non-ASCII input (e.g. a name
//     written entirely in non-Latin script, or a string that consists
//     only of characters SanitizeAgentName replaces with "_" and then
//     trims) collapses to "main" even though the raw input was non-empty.
//     Without this second guard, a user with such a name would silently
//     get `supervisor_<repo>_main` instead of falling through to $USER /
//     hostname.
//
// If every source exhausts, the final `return "main"` is the last-resort
// sentinel — intentional, because the virtual supervisor needs a
// non-empty user slug and "main" is the idiomatic fallback from
// SanitizeAgentName itself.
func resolveUserSlug(repoPath string) string {
	if name := gitUserName(repoPath); name != "" {
		if slug := identity.SanitizeAgentName(name); slug != "main" {
			return slug
		}
	}
	if u := os.Getenv("USER"); u != "" {
		if slug := identity.SanitizeAgentName(u); slug != "main" {
			return slug
		}
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		if slug := identity.SanitizeAgentName(h); slug != "main" {
			return slug
		}
	}
	return "main"
}

// gitUserName returns `git config --get user.name` run in repoPath, or "" on error.
// Delegates to safecmd.GitConfig, which does not apply safecmd.Git's
// `-c user.name=Thrum -c user.email=thrum@local` injection — that
// injection is needed for daemon commit paths but would silently corrupt
// this read.
func gitUserName(repoPath string) string {
	name, err := safecmd.GitConfig(context.Background(), repoPath, "user.name")
	if err != nil {
		return ""
	}
	return name
}

// CleanupLegacySupervisorFiles removes all supervisor pseudo-agent
// identity files from <thrumDir>/identities/. Safe to call on every
// daemon boot — no-op if the directory is missing or contains no
// matching files.
//
// Match condition: file unmarshals to config.IdentityFile with
// Reserved=true AND Agent.Role=="supervisor".
//
// Best-effort: individual read/unmarshal/remove errors are swallowed
// and logged; the function never returns an error. The virtual
// supervisor identity is always available regardless of cleanup
// success.
func CleanupLegacySupervisorFiles(thrumDir string) {
	identitiesDir := filepath.Join(thrumDir, "identities")
	entries, err := os.ReadDir(identitiesDir)
	if err != nil {
		return // missing dir → nothing to clean; other errors → best-effort no-op
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(identitiesDir, e.Name())
		data, err := os.ReadFile(path) // #nosec G304 -- path is <thrumDir>/identities/<entry>
		if err != nil {
			log.Printf("[permission] cleanup: read %s: %v", path, err)
			continue
		}
		var idFile config.IdentityFile
		if err := json.Unmarshal(data, &idFile); err != nil {
			log.Printf("[permission] cleanup: parse %s: %v", path, err)
			continue
		}
		if !idFile.Reserved || idFile.Agent.Role != "supervisor" {
			continue
		}
		if err := os.Remove(path); err != nil {
			log.Printf("[permission] cleanup: remove %s: %v", path, err)
			continue
		}
		log.Printf("[permission] removed legacy supervisor identity file %s", path)
	}
}
