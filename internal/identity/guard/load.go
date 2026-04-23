package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DaemonConfigPath returns the on-disk location of the daemon-level
// identity_guard override, given a .thrum directory. The daemon-level
// override lives under var/ alongside other daemon artifacts
// (thrum.pid, thrum.sock, messages.db) so operators flipping a guard
// per-daemon can do so without touching the committed repo config.
// Exported so tests + docs can reference the canonical path.
func DaemonConfigPath(thrumDir string) string {
	return filepath.Join(thrumDir, "var", "guard-daemon.json")
}

// LoadConfig reads both the repo-level and daemon-level identity_guard
// blocks under thrumDir and returns the merged guard.Config.
//
// Precedence (bottom → top):
//  1. DefaultConfig — every guard strict.
//  2. Repo config at <thrumDir>/config.json, identity_guard block.
//  3. Daemon override at <thrumDir>/var/guard-daemon.json, identity_guard
//     block. Takes precedence field-by-field; unset fields defer to
//     the repo layer (not DefaultConfig).
//
// Missing or malformed files at either layer are silently skipped:
// enforcement defaults on (DefaultConfig is strict), and operator-side
// corruption of the daemon override must NOT lose repo settings.
//
// This is the single loader every guard call site should use; the
// older LoadConfigFromDir is a thin wrapper kept for places that have
// a repo path rather than a thrum directory.
func LoadConfig(thrumDir string) Config {
	repoCfg := readGuardBlock(filepath.Join(thrumDir, "config.json"))
	daemonCfg := readGuardBlock(DaemonConfigPath(thrumDir))
	return Merge(DefaultConfig(), repoCfg, daemonCfg)
}

// readGuardBlock reads the identity_guard block from cfgPath and
// parses it into a Config. Missing or malformed files return a zero
// Config (every Mode ""), so Merge overlays nothing and callers fall
// through to whichever lower layer is present. This intentionally
// does NOT go through ParseConfigFromRaw: that helper pre-populates
// every field with DefaultConfig so a partial JSON would smear strict
// onto otherwise-unset fields, which would then overwrite lower
// layers during Merge.
func readGuardBlock(cfgPath string) Config {
	// #nosec G304 -- cfgPath is derived from caller-supplied thrumDir,
	// not external input.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return Config{}
	}
	var tc struct {
		IdentityGuard *Config `json:"identity_guard,omitempty"`
	}
	if err := json.Unmarshal(data, &tc); err != nil {
		return Config{}
	}
	if tc.IdentityGuard == nil {
		return Config{}
	}
	return *tc.IdentityGuard
}
