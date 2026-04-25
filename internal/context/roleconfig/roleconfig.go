package roleconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CurrentSchemaVersion is the schema version this binary writes when calling
// Save. Bumped when the on-disk shape of role_config changes incompatibly.
const CurrentSchemaVersion = 1

// RoleConfig is the persisted record of /thrum:configure-roles answers,
// stored under the "role_config" top-level key in .thrum/config.json.
type RoleConfig struct {
	SchemaVersion int                     `json:"schema_version"`
	PluginVersion string                  `json:"plugin_version"`
	ConfiguredAt  time.Time               `json:"configured_at"`
	Roles         map[string]RoleSettings `json:"roles"`
}

// RoleSettings carries the per-role choices captured by configure-roles plus
// the rendered_hash drift fingerprint computed at deploy time.
type RoleSettings struct {
	Autonomy     string `json:"autonomy"`
	Scope        string `json:"scope"`
	RenderedHash string `json:"rendered_hash,omitempty"`
}

// Load reads role_config from .thrum/config.json. Returns (nil, nil) when the
// role_config key is absent — caller decides whether that means
// configure-roles has not yet been run.
func Load(thrumDir string) (*RoleConfig, error) {
	path := filepath.Join(thrumDir, "config.json")
	raw, err := os.ReadFile(path) // #nosec G304 -- thrumDir is internal
	if err != nil {
		return nil, fmt.Errorf("read config.json: %w", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("parse config.json: %w", err)
	}
	rc, ok := top["role_config"]
	if !ok {
		return nil, nil
	}
	var cfg RoleConfig
	if err := json.Unmarshal(rc, &cfg); err != nil {
		return nil, fmt.Errorf("parse role_config: %w", err)
	}
	return &cfg, nil
}

// Save atomically writes role_config to .thrum/config.json, preserving every
// other top-level key byte-identical via json.RawMessage round-trip.
//
// Atomicity: write to <path>.tmp then os.Rename so a crash mid-write cannot
// truncate the shared config file (telegram tokens, identity, daemon
// settings would otherwise need manual recovery).
func Save(thrumDir string, cfg *RoleConfig) error {
	path := filepath.Join(thrumDir, "config.json")
	raw, err := os.ReadFile(path) // #nosec G304 -- thrumDir is internal
	if err != nil {
		return fmt.Errorf("read config.json: %w", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("parse config.json: %w", err)
	}
	if top == nil {
		top = make(map[string]json.RawMessage)
	}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal role_config: %w", err)
	}
	top["role_config"] = encoded

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config.json: %w", err)
	}
	out = append(out, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename tmp: %w", err)
	}
	return nil
}
