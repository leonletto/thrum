package guard

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestG5_NoExistingIdentity_Proceeds(t *testing.T) {
	dir := t.TempDir()
	err := G5(&PrimeContext{
		IdentityPath: filepath.Join(dir, "impl_foo.json"),
		ClosestRtPID: 100,
		IsPIDAlive:   func(int) bool { return true },
	})
	if err != nil {
		t.Errorf("want nil (no file), got %v", err)
	}
}

func TestG5_ExistingPIDDead_Proceeds(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 999, "claude")
	err := G5(&PrimeContext{
		IdentityPath: filepath.Join(dir, "impl_foo.json"),
		ClosestRtPID: 100,
		IsPIDAlive:   func(int) bool { return false },
	})
	if err != nil {
		t.Errorf("dead owner should let caller reclaim, got %v", err)
	}
}

func TestG5_ExistingPIDEqualsClosestRuntime_Proceeds(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	err := G5(&PrimeContext{
		IdentityPath: filepath.Join(dir, "impl_foo.json"),
		ClosestRtPID: 100,
		IsPIDAlive:   func(int) bool { return true },
	})
	if err != nil {
		t.Errorf("caller IS the topmost runtime, got %v", err)
	}
}

func TestG5_ExistingPIDNotClosestRuntime_Refuses(t *testing.T) {
	// The identity file records PID 100 as the topmost runtime. The
	// caller's closest runtime is a different PID (200) — i.e. the
	// caller is nested under a subagent whose parent runtime owns
	// the identity. Rejecting this is the whole point of G5.
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	err := G5(&PrimeContext{
		IdentityPath: filepath.Join(dir, "impl_foo.json"),
		ClosestRtPID: 200,
		IsPIDAlive:   func(int) bool { return true },
	})
	if err == nil {
		t.Fatal("want error (caller is not topmost runtime)")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *Error, got %T", err)
	}
	if gErr.Guard != "prime_ownership" {
		t.Errorf("guard=%q", gErr.Guard)
	}
	if gErr.ExpectedPID != 100 {
		t.Errorf("expected_pid=%d", gErr.ExpectedPID)
	}
}

func TestG5_WarnMode_Proceeds(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	err := G5(&PrimeContext{
		Mode:         ModeWarn,
		IdentityPath: filepath.Join(dir, "impl_foo.json"),
		ClosestRtPID: 200,
		IsPIDAlive:   func(int) bool { return true },
	})
	if err != nil {
		t.Errorf("warn mode should proceed, got %v", err)
	}
}

func TestG5_OffMode_NoOp(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_foo", 100, "claude")
	err := G5(&PrimeContext{
		Mode:         ModeOff,
		IdentityPath: filepath.Join(dir, "impl_foo.json"),
		ClosestRtPID: 200,
		IsPIDAlive:   func(int) bool { return true },
	})
	if err != nil {
		t.Errorf("off mode should proceed, got %v", err)
	}
}
