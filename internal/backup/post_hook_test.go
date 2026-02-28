package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunPostBackup_Success(t *testing.T) {
	repoPath := t.TempDir()
	backupDir := t.TempDir()
	currentDir := filepath.Join(backupDir, "current")

	// Command that creates a marker file to prove it ran
	markerFile := filepath.Join(repoPath, "hook-ran.txt")
	command := "echo done > " + markerFile

	result := RunPostBackup(command, repoPath, backupDir, "test-repo", currentDir)

	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
	if result.Command != command {
		t.Errorf("expected command=%q, got %q", command, result.Command)
	}

	if _, err := os.Stat(markerFile); err != nil {
		t.Error("hook did not create marker file")
	}
}

func TestRunPostBackup_EnvVars(t *testing.T) {
	repoPath := t.TempDir()
	backupDir := "/tmp/test-backup"
	repoName := "test-repo"
	currentDir := "/tmp/test-backup/test-repo/current"

	// Command that writes env vars to a file
	envFile := filepath.Join(repoPath, "env.txt")
	command := `echo "$THRUM_BACKUP_DIR|$THRUM_BACKUP_REPO|$THRUM_BACKUP_CURRENT" > ` + envFile

	result := RunPostBackup(command, repoPath, backupDir, repoName, currentDir)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}

	expected := backupDir + "|" + repoName + "|" + currentDir + "\n"
	if string(data) != expected {
		t.Errorf("env vars mismatch:\nexpected: %q\ngot:      %q", expected, string(data))
	}
}

func TestRunPostBackup_Failure(t *testing.T) {
	repoPath := t.TempDir()

	result := RunPostBackup("exit 1", repoPath, "/tmp", "repo", "/tmp/current")

	if result.Error == "" {
		t.Error("expected error for failing command")
	}
}
