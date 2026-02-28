package backup

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LocalExportResult holds counts from a local table export.
type LocalExportResult struct {
	Tables map[string]int // table name → row count
}

// localOnlyTables are the SQLite tables that aren't synced via JSONL.
var localOnlyTables = []string{
	"message_reads",
	"subscriptions",
	"sync_checkpoints",
}

// ExportLocalTables exports local-only SQLite tables as JSONL files.
// Writes to backupDir/local/<table_name>.jsonl.
func ExportLocalTables(db *sql.DB, backupDir string) (LocalExportResult, error) {
	result := LocalExportResult{Tables: make(map[string]int)}

	localDir := filepath.Join(backupDir, "local")
	if err := os.MkdirAll(localDir, 0750); err != nil {
		return result, fmt.Errorf("create local backup dir: %w", err)
	}

	for _, table := range localOnlyTables {
		count, err := exportTable(db, table, filepath.Join(localDir, table+".jsonl"))
		if err != nil {
			return result, fmt.Errorf("export %s: %w", table, err)
		}
		result.Tables[table] = count
	}

	return result, nil
}

// exportTable exports all rows from a table as JSONL using dynamic column scanning.
func exportTable(db *sql.DB, table, outPath string) (int, error) {
	// Use quoted table name to prevent injection (table names come from our constant list)
	rows, err := db.Query(`SELECT * FROM "` + table + `"`) //nolint:gosec // table from hardcoded list
	if err != nil {
		return 0, fmt.Errorf("query %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("get columns for %s: %w", table, err)
	}

	tmpPath := outPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600) //nolint:gosec // G304
	if err != nil {
		return 0, err
	}

	count := 0
	enc := json.NewEncoder(f)

	for rows.Next() {
		// Dynamic column scanning
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}

		if err := rows.Scan(ptrs...); err != nil {
			_ = f.Close()
			_ = os.Remove(tmpPath)
			return 0, fmt.Errorf("scan row in %s: %w", table, err)
		}

		row := make(map[string]any, len(columns))
		for i, col := range columns {
			v := values[i]
			// Convert []byte to string for JSON readability
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[col] = v
		}

		if err := enc.Encode(row); err != nil {
			_ = f.Close()
			_ = os.Remove(tmpPath)
			return 0, fmt.Errorf("encode row in %s: %w", table, err)
		}
		count++
	}

	if err := rows.Err(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return 0, fmt.Errorf("iterate %s: %w", table, err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}

	if err := os.Rename(tmpPath, outPath); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}

	return count, nil
}
