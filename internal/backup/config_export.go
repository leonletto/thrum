package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExportConfigFiles copies config, identity, and context files from thrumDir to the backup.
// Layout:
//
//	thrumDir/config.json           → backupDir/config/config.json
//	thrumDir/identities/*.json     → backupDir/config/identities/*.json
//	thrumDir/context/*.md          → backupDir/config/context/*.md
func ExportConfigFiles(thrumDir, backupDir string) error {
	configBackupDir := filepath.Join(backupDir, "config")
	if err := os.MkdirAll(configBackupDir, 0750); err != nil {
		return fmt.Errorf("create config backup dir: %w", err)
	}

	// Copy config.json
	configSrc := filepath.Join(thrumDir, "config.json")
	if _, err := os.Stat(configSrc); err == nil {
		if _, err := atomicCopyFile(configSrc, filepath.Join(configBackupDir, "config.json")); err != nil {
			return fmt.Errorf("copy config.json: %w", err)
		}
	}

	// Copy identities/*.json
	if err := copyDirFiles(filepath.Join(thrumDir, "identities"), filepath.Join(configBackupDir, "identities"), ".json"); err != nil {
		return fmt.Errorf("copy identities: %w", err)
	}

	// Copy context/*.md
	if err := copyDirFiles(filepath.Join(thrumDir, "context"), filepath.Join(configBackupDir, "context"), ".md"); err != nil {
		return fmt.Errorf("copy context: %w", err)
	}

	return nil
}

// copyDirFiles copies all files with the given suffix from srcDir to dstDir.
// Silently skips if srcDir doesn't exist.
func copyDirFiles(srcDir, dstDir, suffix string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // optional directory
		}
		return err
	}

	if len(entries) == 0 {
		return nil
	}

	if err := os.MkdirAll(dstDir, 0750); err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())
		if _, err := atomicCopyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", entry.Name(), err)
		}
	}

	return nil
}
