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
	return "supervisor_" + resolveLegacyRepoSlug(cfg, repoPath)
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

// resolveLegacyRepoSlug matches the OLD binary's ResolveProjectName
// fallback exactly so the legacy supervisor form is recognizable to the
// receiver compat check. No origin-hash branch here — the old binary
// never used one.
func resolveLegacyRepoSlug(cfg *config.ThrumConfig, repoPath string) string {
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
	out, err := safecmd.Git(context.Background(), repoPath, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	origin := strings.TrimSpace(string(out))
	if origin == "" {
		return ""
	}
	repoID, err := identity.GenerateRepoID(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(repoID, "r_"))
}

// resolveUserSlug selects the owner-identifying half of the supervisor ID.
// Order: git config user.name → $USER → hostname. SanitizeAgentName is
// applied to every source.
func resolveUserSlug(repoPath string) string {
	if name := gitUserName(repoPath); name != "" {
		if slug := identity.SanitizeAgentName(name); slug != "main" { // "main" is SanitizeAgentName's fallback-for-empty
			return slug
		}
	}
	if u := os.Getenv("USER"); u != "" {
		return identity.SanitizeAgentName(u)
	}
	if h, err := os.Hostname(); err == nil {
		return identity.SanitizeAgentName(h)
	}
	return "main"
}

// gitUserName returns `git config --get user.name` run in repoPath, or "" on error.
func gitUserName(repoPath string) string {
	out, err := safecmd.Git(context.Background(), repoPath, "config", "--get", "user.name")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
