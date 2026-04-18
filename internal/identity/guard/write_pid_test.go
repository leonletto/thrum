package guard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWritePID_AtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := writeIdentityFile(t, dir, "impl_foo", 111, "claude")
	if err := WritePID(path, 222); err != nil {
		t.Fatal(err)
	}
	got := loadIdentityForTest(t, path)
	if got.AgentPID != 222 {
		t.Errorf("pid = %d, want 222", got.AgentPID)
	}
	// Other fields must be preserved verbatim.
	if got.Agent.Name != "impl_foo" {
		t.Errorf("agent.name not preserved: %q", got.Agent.Name)
	}
	if got.Runtime != "claude" {
		t.Errorf("runtime not preserved: %q", got.Runtime)
	}
}

func TestWritePID_FreshWrite(t *testing.T) {
	// Writing to a non-existent file is the first-prime path. The
	// resulting file should contain only AgentPID + UpdatedAt.
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_bar.json")
	if err := WritePID(path, 333); err != nil {
		t.Fatal(err)
	}
	got := loadIdentityForTest(t, path)
	if got.AgentPID != 333 {
		t.Errorf("pid = %d, want 333", got.AgentPID)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be stamped on write")
	}
}

func TestWritePID_BumpsUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	path := writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	before := loadIdentityForTest(t, path).UpdatedAt
	time.Sleep(5 * time.Millisecond)
	if err := WritePID(path, 200); err != nil {
		t.Fatal(err)
	}
	after := loadIdentityForTest(t, path).UpdatedAt
	if !after.After(before) {
		t.Errorf("UpdatedAt should advance: before=%v after=%v", before, after)
	}
}

func TestWritePID_ParentMissingErrors(t *testing.T) {
	err := WritePID("/definitely/does/not/exist/x.json", 1)
	if err == nil {
		t.Fatal("want error for missing parent dir")
	}
	// Cleanup in case a stray partial was written somehow.
	_ = os.Remove("/definitely/does/not/exist/x.json")
}
