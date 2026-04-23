package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Identity is the runtime view of the daemon's identity block.
type Identity struct {
	DaemonID     string `json:"daemon_id"`
	RepoName     string `json:"repo_name"`
	Hostname     string `json:"hostname"`
	RepoPath     string `json:"repo_path"`
	GitOriginURL string `json:"git_origin_url"`
	InitAt       string `json:"init_at"`
}

// Bootstrap loads or creates the daemon identity stored in
// <thrumDir>/config.json under the "identity" key. Callers:
//   - thrum init (explicit creation)
//   - daemon startup (lazy backfill for pre-identity installs)
//
// Behavior:
//   - Empty daemon_id → generate ULID, set init_at, populate metadata.
//   - Legacy hostname-derived daemon_id → rotate to ULID, log WARN.
//   - Existing ULID → refresh metadata (hostname, repo_path, repo_name)
//     in-place, do not rotate.
//
// Config.json is always written back atomically.
func Bootstrap(thrumDir, repoPath string) (Identity, error) {
	cfgPath := filepath.Join(thrumDir, "config.json")

	raw, readErr := os.ReadFile(cfgPath) // #nosec G304 -- cfgPath is derived from thrumDir, not untrusted input
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return Identity{}, fmt.Errorf("read config.json: %w", readErr)
	}

	var cfg map[string]json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Identity{}, fmt.Errorf("parse config.json: %w", err)
		}
	}
	if cfg == nil {
		cfg = make(map[string]json.RawMessage)
	}

	var existing Identity
	if blob, ok := cfg["identity"]; ok && len(blob) > 0 {
		if err := json.Unmarshal(blob, &existing); err != nil {
			return Identity{}, fmt.Errorf("parse identity block: %w", err)
		}
	}

	host, _ := os.Hostname()
	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		absRepo = repoPath
	}

	out := existing
	out.Hostname = host
	out.RepoPath = absRepo
	if out.RepoName == "" || filepath.Base(out.RepoPath) != filepath.Base(absRepo) {
		out.RepoName = filepath.Base(absRepo)
	}

	switch {
	case out.DaemonID == "":
		out.DaemonID = GenerateDaemonID()
		out.InitAt = time.Now().UTC().Format(time.RFC3339)
		out.GitOriginURL = readGitOriginURL(absRepo)
	case IsLegacyDaemonID(out.DaemonID, host):
		old := out.DaemonID
		out.DaemonID = GenerateDaemonID()
		out.InitAt = time.Now().UTC().Format(time.RFC3339)
		log.Printf("[identity] daemon_id rotated: %s -> %s (reason: legacy hostname-derived)", old, out.DaemonID)
		log.Printf("[identity] Paired peers must be re-paired manually. On each peer host:")
		log.Printf("[identity]   thrum peer remove <this-hostname>")
		log.Printf("[identity]   thrum peer add <this-hostname> ...")
		if out.GitOriginURL == "" {
			out.GitOriginURL = readGitOriginURL(absRepo)
		}
	default:
		// Valid ULID. Leave daemon_id and init_at alone.
	}

	// Back up config.json before the first identity write (new or rotated id).
	// Backup-once: never overwrite an existing .pre-identity-bak so operator
	// can always revert by renaming the backup back to config.json.
	changing := out.DaemonID != existing.DaemonID || out.InitAt != existing.InitAt
	if changing {
		if err := backupConfigOnce(cfgPath, cfgPath+".pre-identity-bak"); err != nil {
			log.Printf("[identity] config.json backup failed (upgrade proceeding): %v", err)
		}
	}

	blob, err := json.Marshal(out)
	if err != nil {
		return Identity{}, fmt.Errorf("marshal identity: %w", err)
	}
	cfg["identity"] = blob

	if err := writeConfigAtomic(cfgPath, cfg); err != nil {
		return Identity{}, err
	}

	return out, nil
}

// readGitOriginURL returns `git config --get remote.origin.url` for repoPath,
// or empty string if the command fails (not a repo, no origin, etc.).
func readGitOriginURL(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// backupConfigOnce copies src to dst if src exists and dst does not.
// Returns nil (no-op) if dst exists or src does not exist. Errors on copy failure.
func backupConfigOnce(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil // backup already exists — don't overwrite pre-upgrade state
	}
	data, err := os.ReadFile(src) // #nosec G304 -- src is a config.json path derived from thrumDir
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // nothing to back up on fresh install
		}
		return fmt.Errorf("read for backup: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	log.Printf("[identity] backed up pre-upgrade config to %s", dst)
	return nil
}

// writeConfigAtomic marshals cfg to JSON (indented) and writes to path via
// temp-file + rename so an interrupted write cannot truncate config.json.
// The output preserves existing top-level keys that weren't touched.
func writeConfigAtomic(path string, cfg map[string]json.RawMessage) error {
	ordered := make(map[string]any, len(cfg))
	for k, v := range cfg {
		ordered[k] = v
	}
	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "config.json.tmp-*")
	if err != nil {
		return fmt.Errorf("temp config: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}
