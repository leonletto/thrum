package backup

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/config"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// BackupOptions configures a backup run.
type BackupOptions struct {
	BackupDir    string                 // resolved backup directory
	RepoName     string                 // used as subfolder name
	SyncDir      string                 // path to a-sync worktree
	ThrumDir     string                 // path to .thrum directory
	DBPath       string                 // path to messages.db
	ThrumVersion string                 // version string for manifest
	Retention    *config.RetentionConfig // optional: apply GFS rotation after backup
	Plugins      []config.PluginConfig  // optional: third-party backup plugins
}

// BackupResult holds the outcome of a backup run.
type BackupResult struct {
	CurrentDir    string
	Manifest      *Manifest
	SyncResult    SyncExportResult
	LocalResult   LocalExportResult
	PluginResults []PluginResult
}

// RunBackup orchestrates a full backup: exports JSONL, SQLite local tables,
// config files, and writes a manifest.
func RunBackup(opts BackupOptions) (*BackupResult, error) {
	if opts.BackupDir == "" {
		return nil, fmt.Errorf("backup directory is required")
	}
	if opts.RepoName == "" {
		return nil, fmt.Errorf("repo name is required")
	}

	repoDir := filepath.Join(opts.BackupDir, opts.RepoName)
	currentDir := filepath.Join(repoDir, "current")
	archivesDir := filepath.Join(repoDir, "archives")

	// Archive existing current/ before writing new backup
	if _, err := CompressCurrentToArchive(currentDir, archivesDir); err != nil {
		return nil, fmt.Errorf("archive previous backup: %w", err)
	}

	if err := os.MkdirAll(currentDir, 0750); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	result := &BackupResult{CurrentDir: currentDir}

	// 1. Export JSONL from sync worktree
	if opts.SyncDir != "" {
		syncResult, err := ExportSyncData(opts.SyncDir, currentDir)
		if err != nil {
			return nil, fmt.Errorf("export sync data: %w", err)
		}
		result.SyncResult = syncResult
	}

	// 2. Export local-only SQLite tables
	if opts.DBPath != "" {
		if _, err := os.Stat(opts.DBPath); err == nil {
			db, err := sql.Open("sqlite", opts.DBPath)
			if err != nil {
				return nil, fmt.Errorf("open database: %w", err)
			}
			defer func() { _ = db.Close() }()

			localResult, err := ExportLocalTables(db, currentDir)
			if err != nil {
				return nil, fmt.Errorf("export local tables: %w", err)
			}
			result.LocalResult = localResult
		}
	}

	// 3. Export config, identity, and context files
	if opts.ThrumDir != "" {
		if err := ExportConfigFiles(opts.ThrumDir, currentDir); err != nil {
			return nil, fmt.Errorf("export config files: %w", err)
		}
	}

	// 4. Run plugins (non-fatal: failures are logged in results)
	if len(opts.Plugins) > 0 {
		pluginResults, err := RunPlugins(opts.Plugins, opts.SyncDir, currentDir)
		if err != nil {
			return nil, fmt.Errorf("run plugins: %w", err)
		}
		result.PluginResults = pluginResults
	}

	// 5. Write manifest
	configFiles := 0
	configDir := filepath.Join(currentDir, "config")
	if entries, err := os.ReadDir(configDir); err == nil {
		configFiles = countFiles(entries)
		// Count subdirectories
		for _, e := range entries {
			if e.IsDir() {
				subPath := filepath.Join(configDir, e.Name())
				if subEntries, err := os.ReadDir(subPath); err == nil {
					configFiles += countFiles(subEntries)
				}
			}
		}
	}

	manifest := &Manifest{
		Version:      1,
		Timestamp:    time.Now().UTC(),
		ThrumVersion: opts.ThrumVersion,
		RepoName:     opts.RepoName,
		Counts: ManifestCounts{
			Events:       result.SyncResult.EventLines,
			MessageFiles: result.SyncResult.MessageFiles,
			LocalTables:  len(result.LocalResult.Tables),
			ConfigFiles:  configFiles,
			Plugins:      PluginNames(result.PluginResults),
		},
	}

	if err := WriteManifest(currentDir, manifest); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	result.Manifest = manifest

	// 6. Apply GFS retention if configured
	if opts.Retention != nil {
		if err := ApplyRetention(archivesDir, *opts.Retention); err != nil {
			return nil, fmt.Errorf("apply retention: %w", err)
		}
	}

	return result, nil
}

func countFiles(entries []os.DirEntry) int {
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}
