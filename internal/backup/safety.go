package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CreateSafetyBackup creates a pre-restore safety backup of the current/ directory.
// Returns the path to the created zip, or empty string if there was nothing to protect.
// Safety backups use the "pre-restore-" prefix which exempts them from GFS rotation.
func CreateSafetyBackup(backupDir, repoName string) (string, error) {
	currentDir := filepath.Join(backupDir, repoName, "current")

	// Check if current/ exists and has content
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // nothing to protect
		}
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}

	archivesDir := filepath.Join(backupDir, repoName, "archives")
	if err := os.MkdirAll(archivesDir, 0750); err != nil {
		return "", fmt.Errorf("create archives dir: %w", err)
	}

	timestamp := time.Now().UTC().Format(archiveTimeFormat)
	zipPath := filepath.Join(archivesDir, "pre-restore-"+timestamp+".zip")
	tmpPath := zipPath + ".tmp"

	if err := createZip(currentDir, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("create safety backup: %w", err)
	}

	if err := os.Rename(tmpPath, zipPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	return zipPath, nil
}
