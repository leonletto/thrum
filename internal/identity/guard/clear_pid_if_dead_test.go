package guard

import (
	"os"
	"testing"
)

func TestClearPIDIfDead_LivePID_NoOp(t *testing.T) {
	dir := t.TempDir()
	livePID := os.Getpid() // this test process is definitionally alive
	path := writeIdentityFile(t, dir, "impl_foo", livePID, "claude")

	cleared, err := ClearPIDIfDead(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleared {
		t.Fatalf("expected cleared=false for live PID, got true")
	}
	id := loadIdentityForTest(t, path)
	if id.AgentPID != livePID {
		t.Fatalf("expected AgentPID unchanged (%d), got %d", livePID, id.AgentPID)
	}
}

func TestClearPIDIfDead_DeadPID_Clears(t *testing.T) {
	dir := t.TempDir()
	// PID 2147483646 is well above any real PID and guaranteed dead on all
	// supported platforms (Linux max pid_t is typically 99999; macOS is ~8M).
	path := writeIdentityFile(t, dir, "impl_foo", 2147483646, "claude")

	cleared, err := ClearPIDIfDead(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cleared {
		t.Fatalf("expected cleared=true for dead PID, got false")
	}
	id := loadIdentityForTest(t, path)
	if id.AgentPID != 0 {
		t.Fatalf("expected AgentPID cleared to 0, got %d", id.AgentPID)
	}
}

func TestClearPIDIfDead_ZeroPID_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := writeIdentityFile(t, dir, "impl_foo", 0, "claude")

	cleared, err := ClearPIDIfDead(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleared {
		t.Fatalf("expected cleared=false for already-zero PID, got true")
	}
}

func TestClearPIDIfDead_MissingFile_Errors(t *testing.T) {
	_, err := ClearPIDIfDead("/nonexistent/path/to/identity.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
