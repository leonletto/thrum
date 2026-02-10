package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWritePIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	if err := WritePIDFile(pidPath); err != nil {
		t.Fatalf("WritePIDFile failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		t.Fatal("PID file was not created")
	}

	// Verify file content
	pid, err := ReadPIDFile(pidPath)
	if err != nil {
		t.Fatalf("ReadPIDFile failed: %v", err)
	}

	if pid != os.Getpid() {
		t.Fatalf("PID mismatch: got %d, want %d", pid, os.Getpid())
	}
}

func TestWritePIDFileCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "subdir", "test.pid")

	if err := WritePIDFile(pidPath); err != nil {
		t.Fatalf("WritePIDFile failed: %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(filepath.Dir(pidPath)); os.IsNotExist(err) {
		t.Fatal("PID file directory was not created")
	}
}

func TestReadPIDFileNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "nonexistent.pid")

	_, err := ReadPIDFile(pidPath)
	if err == nil {
		t.Fatal("expected error reading non-existent PID file")
	}
}

func TestReadPIDFileInvalidContent(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write invalid content
	if err := os.WriteFile(pidPath, []byte("not-a-number\n"), 0600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := ReadPIDFile(pidPath)
	if err == nil {
		t.Fatal("expected error reading invalid PID file")
	}
}

func TestCheckPIDFileRunning(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write current process PID
	if err := WritePIDFile(pidPath); err != nil {
		t.Fatalf("WritePIDFile failed: %v", err)
	}

	// Check PID file
	running, pid, err := CheckPIDFile(pidPath)
	if err != nil {
		t.Fatalf("CheckPIDFile failed: %v", err)
	}

	if !running {
		t.Fatal("expected process to be running")
	}

	if pid != os.Getpid() {
		t.Fatalf("PID mismatch: got %d, want %d", pid, os.Getpid())
	}
}

func TestCheckPIDFileStale(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write a PID that doesn't exist (very high number unlikely to be used)
	stalePID := 999999
	if err := os.WriteFile(pidPath, []byte("999999\n"), 0600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Check PID file
	running, pid, err := CheckPIDFile(pidPath)
	if err != nil {
		t.Fatalf("CheckPIDFile failed: %v", err)
	}

	if running {
		t.Fatal("expected process to not be running (stale PID)")
	}

	if pid != stalePID {
		t.Fatalf("PID mismatch: got %d, want %d", pid, stalePID)
	}
}

func TestCheckPIDFileNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "nonexistent.pid")

	// Check non-existent PID file
	running, pid, err := CheckPIDFile(pidPath)
	if err != nil {
		t.Fatalf("CheckPIDFile failed: %v", err)
	}

	if running {
		t.Fatal("expected running to be false for non-existent PID file")
	}

	if pid != 0 {
		t.Fatalf("expected PID to be 0 for non-existent file, got %d", pid)
	}
}

func TestRemovePIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write PID file
	if err := WritePIDFile(pidPath); err != nil {
		t.Fatalf("WritePIDFile failed: %v", err)
	}

	// Remove PID file
	if err := RemovePIDFile(pidPath); err != nil {
		t.Fatalf("RemovePIDFile failed: %v", err)
	}

	// Verify file was removed
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatal("PID file was not removed")
	}
}

func TestRemovePIDFileNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "nonexistent.pid")

	// Remove non-existent file should not error
	if err := RemovePIDFile(pidPath); err != nil {
		t.Fatalf("RemovePIDFile failed on non-existent file: %v", err)
	}
}

func TestPIDFileWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Step 1: No PID file exists
	running, pid, err := CheckPIDFile(pidPath)
	if err != nil {
		t.Fatalf("CheckPIDFile failed: %v", err)
	}
	if running || pid != 0 {
		t.Fatal("expected no running process initially")
	}

	// Step 2: Write PID file
	if err := WritePIDFile(pidPath); err != nil {
		t.Fatalf("WritePIDFile failed: %v", err)
	}

	// Step 3: Verify process is running
	running, pid, err = CheckPIDFile(pidPath)
	if err != nil {
		t.Fatalf("CheckPIDFile failed: %v", err)
	}
	if !running {
		t.Fatal("expected process to be running")
	}
	if pid != os.Getpid() {
		t.Fatalf("PID mismatch: got %d, want %d", pid, os.Getpid())
	}

	// Step 4: Remove PID file
	if err := RemovePIDFile(pidPath); err != nil {
		t.Fatalf("RemovePIDFile failed: %v", err)
	}

	// Step 5: Verify file is gone
	running, pid, err = CheckPIDFile(pidPath)
	if err != nil {
		t.Fatalf("CheckPIDFile failed: %v", err)
	}
	if running || pid != 0 {
		t.Fatal("expected no running process after removal")
	}
}

func TestIsProcessRunning(t *testing.T) {
	// Test with current process (should be running)
	running := isProcessRunning(os.Getpid())
	if !running {
		t.Fatal("expected current process to be running")
	}

	// Test with PID that doesn't exist
	running = isProcessRunning(999999)
	if running {
		t.Fatal("expected non-existent process to not be running")
	}
}

// ====== JSON PID File Tests ======

func TestWritePIDFileJSON(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	info := PIDInfo{
		PID:        os.Getpid(),
		RepoPath:   "/test/repo",
		StartedAt:  time.Now().UTC(),
		SocketPath: "/test/repo/.thrum/var/thrum.sock",
	}

	if err := WritePIDFileJSON(pidPath, info); err != nil {
		t.Fatalf("WritePIDFileJSON failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		t.Fatal("PID file was not created")
	}

	// Verify file content is valid JSON
	data, err := os.ReadFile(pidPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read PID file: %v", err)
	}

	var readInfo PIDInfo
	if err := json.Unmarshal(data, &readInfo); err != nil {
		t.Fatalf("PID file is not valid JSON: %v", err)
	}

	// Verify fields
	if readInfo.PID != info.PID {
		t.Fatalf("PID mismatch: got %d, want %d", readInfo.PID, info.PID)
	}
	if readInfo.RepoPath != info.RepoPath {
		t.Fatalf("RepoPath mismatch: got %s, want %s", readInfo.RepoPath, info.RepoPath)
	}
	if readInfo.SocketPath != info.SocketPath {
		t.Fatalf("SocketPath mismatch: got %s, want %s", readInfo.SocketPath, info.SocketPath)
	}
}

func TestReadPIDFileJSON_JSONFormat(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write a JSON PID file
	original := PIDInfo{
		PID:        12345,
		RepoPath:   "/test/repo",
		StartedAt:  time.Now().UTC().Truncate(time.Second),
		SocketPath: "/test/sock",
	}

	data, _ := json.Marshal(original)
	if err := os.WriteFile(pidPath, data, 0600); err != nil {
		t.Fatalf("Failed to write test PID file: %v", err)
	}

	// Read it back
	info, err := ReadPIDFileJSON(pidPath)
	if err != nil {
		t.Fatalf("ReadPIDFileJSON failed: %v", err)
	}

	// Verify fields
	if info.PID != original.PID {
		t.Fatalf("PID mismatch: got %d, want %d", info.PID, original.PID)
	}
	if info.RepoPath != original.RepoPath {
		t.Fatalf("RepoPath mismatch: got %s, want %s", info.RepoPath, original.RepoPath)
	}
	if info.SocketPath != original.SocketPath {
		t.Fatalf("SocketPath mismatch: got %s, want %s", info.SocketPath, original.SocketPath)
	}
}

func TestReadPIDFileJSON_PlainIntegerFormat(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write a plain integer PID file (old format)
	if err := os.WriteFile(pidPath, []byte("12345\n"), 0600); err != nil {
		t.Fatalf("Failed to write test PID file: %v", err)
	}

	// Read it back
	info, err := ReadPIDFileJSON(pidPath)
	if err != nil {
		t.Fatalf("ReadPIDFileJSON failed: %v", err)
	}

	// Verify PID is read correctly
	if info.PID != 12345 {
		t.Fatalf("PID mismatch: got %d, want 12345", info.PID)
	}

	// Verify other fields are empty (backward compat)
	if info.RepoPath != "" {
		t.Fatalf("RepoPath should be empty for plain format, got %s", info.RepoPath)
	}
	if info.SocketPath != "" {
		t.Fatalf("SocketPath should be empty for plain format, got %s", info.SocketPath)
	}
}

func TestCheckPIDFileJSON_Running(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write current process info
	info := PIDInfo{
		PID:      os.Getpid(),
		RepoPath: "/test/repo",
	}

	if err := WritePIDFileJSON(pidPath, info); err != nil {
		t.Fatalf("WritePIDFileJSON failed: %v", err)
	}

	// Check PID file
	running, readInfo, err := CheckPIDFileJSON(pidPath)
	if err != nil {
		t.Fatalf("CheckPIDFileJSON failed: %v", err)
	}

	if !running {
		t.Fatal("expected process to be running")
	}

	if readInfo.PID != os.Getpid() {
		t.Fatalf("PID mismatch: got %d, want %d", readInfo.PID, os.Getpid())
	}

	if readInfo.RepoPath != "/test/repo" {
		t.Fatalf("RepoPath mismatch: got %s, want /test/repo", readInfo.RepoPath)
	}
}

func TestCheckPIDFileJSON_Stale(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "test.pid")

	// Write a PID that doesn't exist
	info := PIDInfo{
		PID:      999999,
		RepoPath: "/test/repo",
	}

	if err := WritePIDFileJSON(pidPath, info); err != nil {
		t.Fatalf("WritePIDFileJSON failed: %v", err)
	}

	// Check PID file
	running, readInfo, err := CheckPIDFileJSON(pidPath)
	if err != nil {
		t.Fatalf("CheckPIDFileJSON failed: %v", err)
	}

	if running {
		t.Fatal("expected process to not be running (stale PID)")
	}

	if readInfo.PID != 999999 {
		t.Fatalf("PID mismatch: got %d, want 999999", readInfo.PID)
	}
}

func TestCheckPIDFileJSON_NotExist(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "nonexistent.pid")

	// Check non-existent PID file
	running, info, err := CheckPIDFileJSON(pidPath)
	if err != nil {
		t.Fatalf("CheckPIDFileJSON failed: %v", err)
	}

	if running {
		t.Fatal("expected running to be false for non-existent PID file")
	}

	if info.PID != 0 {
		t.Fatalf("expected PID to be 0 for non-existent file, got %d", info.PID)
	}
}

func TestValidatePIDRepo(t *testing.T) {
	tests := []struct {
		name     string
		info     PIDInfo
		expected string
		want     bool
	}{
		{
			name:     "matching repo paths",
			info:     PIDInfo{PID: 123, RepoPath: "/test/repo"},
			expected: "/test/repo",
			want:     true,
		},
		{
			name:     "different repo paths",
			info:     PIDInfo{PID: 123, RepoPath: "/test/repo1"},
			expected: "/test/repo2",
			want:     false,
		},
		{
			name:     "empty repo path in PID file (legacy — cannot confirm match)",
			info:     PIDInfo{PID: 123, RepoPath: ""},
			expected: "/test/repo",
			want:     false,
		},
		{
			name:     "empty expected path with non-empty PID repo",
			info:     PIDInfo{PID: 123, RepoPath: "/test/repo"},
			expected: "",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidatePIDRepo(tt.info, tt.expected)
			if got != tt.want {
				t.Errorf("ValidatePIDRepo() = %v, want %v", got, tt.want)
			}
		})
	}
}
