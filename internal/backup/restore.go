package backup

import (
	"archive/zip"
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
)

// RestoreOptions configures a restore operation.
type RestoreOptions struct {
	BackupDir   string                 // resolved backup directory
	RepoName    string                 // repo subfolder name
	ArchivePath string                 // optional: specific zip to restore from (empty = use current/)
	SyncDir     string                 // path to a-sync worktree to restore JSONL into
	ThrumDir    string                 // path to .thrum directory
	DBPath      string                 // path to messages.db
	Plugins     []config.PluginConfig  // optional: plugins with restore commands
	RepoPath    string                 // project repo root (CWD for plugin restore commands)
}

// RestoreResult holds the outcome of a restore.
type RestoreResult struct {
	SafetyBackup string // path to pre-restore zip, if created
	Source       string // "current" or the archive path
}

// RunRestore restores thrum data from a backup.
//  1. Creates a safety backup if existing data is present
//  2. Determines source (archive zip or current/)
//  3. Copies JSONL files to sync worktree
//  4. Imports local tables into SQLite
//  5. Restores config files to .thrum/
//  6. Removes messages.db so projector rebuilds on next daemon start
func RunRestore(opts RestoreOptions) (*RestoreResult, error) {
	if opts.BackupDir == "" {
		return nil, fmt.Errorf("backup directory is required")
	}
	if opts.RepoName == "" {
		return nil, fmt.Errorf("repo name is required")
	}

	result := &RestoreResult{}

	// 1. Safety backup
	safetyPath, err := CreateSafetyBackup(opts.BackupDir, opts.RepoName)
	if err != nil {
		return nil, fmt.Errorf("create safety backup: %w", err)
	}
	result.SafetyBackup = safetyPath

	// 2. Determine source directory
	var sourceDir string
	if opts.ArchivePath != "" {
		// Extract zip to temp dir
		tmpDir, err := os.MkdirTemp("", "thrum-restore-*")
		if err != nil {
			return nil, fmt.Errorf("create temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		if err := extractZip(opts.ArchivePath, tmpDir); err != nil {
			return nil, fmt.Errorf("extract archive: %w", err)
		}
		sourceDir = tmpDir
		result.Source = opts.ArchivePath
	} else {
		sourceDir = filepath.Join(opts.BackupDir, opts.RepoName, "current")
		result.Source = "current"
	}

	// Verify source exists
	if _, err := os.Stat(sourceDir); err != nil {
		return nil, fmt.Errorf("backup source not found: %w", err)
	}

	// 3. Restore JSONL to sync worktree
	if opts.SyncDir != "" {
		if err := restoreSyncData(sourceDir, opts.SyncDir); err != nil {
			return nil, fmt.Errorf("restore sync data: %w", err)
		}
	}

	// 4. Import local tables
	if opts.DBPath != "" {
		localDir := filepath.Join(sourceDir, "local")
		if _, err := os.Stat(localDir); err == nil {
			if err := ImportLocalTables(opts.DBPath, localDir); err != nil {
				return nil, fmt.Errorf("import local tables: %w", err)
			}
		}
	}

	// 5. Restore config files
	if opts.ThrumDir != "" {
		if err := restoreConfigFiles(sourceDir, opts.ThrumDir); err != nil {
			return nil, fmt.Errorf("restore config files: %w", err)
		}
	}

	// 6. Run plugin restore commands (non-fatal)
	if len(opts.Plugins) > 0 && opts.RepoPath != "" {
		pluginDir := filepath.Join(sourceDir, "plugins")
		for _, p := range opts.Plugins {
			if p.Command == "" {
				continue
			}
			// Set CWD to plugin backup dir if it exists, otherwise repo root
			cwd := opts.RepoPath
			pDir := filepath.Join(pluginDir, p.Name)
			if _, err := os.Stat(pDir); err == nil {
				cwd = pDir
			}
			hookResult := RunPostBackup(p.Command, cwd, opts.BackupDir, opts.RepoName, sourceDir)
			if hookResult.Error != "" {
				fmt.Fprintf(os.Stderr, "Warning: plugin %s restore failed: %s\n", p.Name, hookResult.Error)
			}
		}
	}

	// 7. Remove messages.db so projector rebuilds on daemon start
	if opts.DBPath != "" {
		_ = os.Remove(opts.DBPath)
		// Also remove WAL/SHM files
		_ = os.Remove(opts.DBPath + "-wal")
		_ = os.Remove(opts.DBPath + "-shm")
	}

	return result, nil
}

// restoreSyncData copies JSONL files from backup source to sync worktree.
func restoreSyncData(sourceDir, syncDir string) error {
	// Copy events.jsonl
	eventsPath := filepath.Join(sourceDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); err == nil {
		if err := os.MkdirAll(syncDir, 0750); err != nil {
			return err
		}
		if _, err := atomicCopyFile(eventsPath, filepath.Join(syncDir, "events.jsonl")); err != nil {
			return fmt.Errorf("copy events.jsonl: %w", err)
		}
	}

	// Copy messages/*.jsonl
	msgSrcDir := filepath.Join(sourceDir, "messages")
	entries, err := os.ReadDir(msgSrcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	msgDstDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(msgDstDir, 0750); err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		src := filepath.Join(msgSrcDir, entry.Name())
		dst := filepath.Join(msgDstDir, entry.Name())
		if _, err := atomicCopyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// restoreConfigFiles copies config, identity, and context files from backup to thrum dir.
func restoreConfigFiles(sourceDir, thrumDir string) error {
	configSrcDir := filepath.Join(sourceDir, "config")
	if _, err := os.Stat(configSrcDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Restore config.json
	configSrc := filepath.Join(configSrcDir, "config.json")
	if _, err := os.Stat(configSrc); err == nil {
		if _, err := atomicCopyFile(configSrc, filepath.Join(thrumDir, "config.json")); err != nil {
			return fmt.Errorf("restore config.json: %w", err)
		}
	}

	// Restore identities
	if err := copyDirFiles(filepath.Join(configSrcDir, "identities"), filepath.Join(thrumDir, "identities"), ".json"); err != nil {
		return fmt.Errorf("restore identities: %w", err)
	}

	// Restore context
	if err := copyDirFiles(filepath.Join(configSrcDir, "context"), filepath.Join(thrumDir, "context"), ".md"); err != nil {
		return fmt.Errorf("restore context: %w", err)
	}

	return nil
}

// ImportLocalTables reads JSONL files from localDir and inserts rows into the SQLite database.
func ImportLocalTables(dbPath, localDir string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	for _, table := range localOnlyTables {
		jsonlPath := filepath.Join(localDir, table+".jsonl")
		if _, err := os.Stat(jsonlPath); err != nil {
			continue // skip missing tables
		}
		if err := importTable(db, table, jsonlPath); err != nil {
			return fmt.Errorf("import %s: %w", table, err)
		}
	}

	return nil
}

// importTable reads a JSONL file and inserts rows into the given table.
func importTable(db *sql.DB, table, jsonlPath string) error {
	// Build allowlist of valid column names from table schema
	validCols, err := getTableColumns(db, table)
	if err != nil {
		return fmt.Errorf("get columns for %s: %w", table, err)
	}

	f, err := os.Open(jsonlPath) //nolint:gosec // G304 - internal path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op if committed

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10 MB max line size
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var row map[string]any
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("parse line: %w", err)
		}

		if len(row) == 0 {
			continue
		}

		// Build INSERT OR IGNORE statement using only validated columns
		columns := make([]string, 0, len(row))
		placeholders := make([]string, 0, len(row))
		values := make([]any, 0, len(row))
		for col, val := range row {
			if !validCols[col] {
				continue // skip unknown columns
			}
			columns = append(columns, `"`+strings.ReplaceAll(col, `"`, `""`)+`"`)
			placeholders = append(placeholders, "?")
			values = append(values, val)
		}

		if len(columns) == 0 {
			continue
		}

		query := fmt.Sprintf(`INSERT OR IGNORE INTO "%s" (%s) VALUES (%s)`,
			table, strings.Join(columns, ", "), strings.Join(placeholders, ", "))

		if _, err := tx.Exec(query, values...); err != nil {
			return fmt.Errorf("insert into %s: %w", table, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

// getTableColumns returns the set of valid column names for a table.
func getTableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info("` + table + `")`) //nolint:gosec // table name from hardcoded localOnlyTables
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// extractZip extracts a zip file to the destination directory.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		// Zip slip protection: reject paths with directory traversal
		rel := filepath.Clean(f.Name)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}
		destPath := filepath.Join(destDir, rel) //nolint:gosec // G305 - validated above

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0750); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		outFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode()) //nolint:gosec // G304
		if err != nil {
			_ = rc.Close()
			return err
		}

		const maxExtractedBytes = 2 << 30 // 2 GiB per file
		if _, err := io.Copy(outFile, io.LimitReader(rc, maxExtractedBytes)); err != nil {
			_ = outFile.Close()
			_ = rc.Close()
			return err
		}

		_ = outFile.Close()
		_ = rc.Close()
	}

	return nil
}
