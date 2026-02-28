package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Manifest records metadata about a backup snapshot.
type Manifest struct {
	Version      int            `json:"version"`
	Timestamp    time.Time      `json:"timestamp"`
	ThrumVersion string         `json:"thrum_version"`
	RepoName     string         `json:"repo_name"`
	Counts       ManifestCounts `json:"counts"`
}

// ManifestCounts holds item counts in the backup.
type ManifestCounts struct {
	Events       int      `json:"events"`
	MessageFiles int      `json:"message_files"`
	LocalTables  int      `json:"local_tables"`
	ConfigFiles  int      `json:"config_files"`
	Plugins      []string `json:"plugins,omitempty"`
}

// WriteManifest writes a manifest.json to the given directory.
func WriteManifest(dir string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')

	tmpPath := filepath.Join(dir, "manifest.json.tmp")
	outPath := filepath.Join(dir, "manifest.json")

	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// ReadManifest reads a manifest.json from the given directory.
func ReadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // G304
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}
