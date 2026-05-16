//go:build unix

package peercred

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProcessCWD_SelfPID verifies processCWD correctly resolves the
// current process's working directory using the platform's native
// API. The platform-specific implementations live in resolver_cwd_darwin.go
// (libproc syscall) and resolver_cwd_other.go (gopsutil); this test
// exercises whichever is built and asserts the answer matches os.Getwd().
//
// Regression coverage for the rc.5 macOS fix (thrum-2t7d et al.): from
// sec.2 through v0.10.3-rc.4, processCWD on Darwin delegated to gopsutil
// which is documented as "not implemented yet" on Darwin and returned an
// error for every call. This test would have caught that immediately.
func TestProcessCWD_SelfPID(t *testing.T) {
	want, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", want, err)
	}

	got, err := processCWD(os.Getpid())
	if err != nil {
		t.Fatalf("processCWD(self pid): unexpected error: %v", err)
	}
	if got == "" {
		t.Fatalf("processCWD(self pid): returned empty cwd")
	}

	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", got, err)
	}

	// Compare canonicalized paths so /tmp vs /private/tmp on Darwin
	// (and similar symlink rewrites elsewhere) don't false-fail the test.
	if gotResolved != wantResolved {
		t.Errorf("processCWD(self pid) = %q (canonical %q), want %q (canonical %q)",
			got, gotResolved, want, wantResolved)
	}
}

// TestProcessCWD_NonexistentPID verifies processCWD returns an error (not
// an empty string and not the current process's cwd) when given a PID
// that doesn't exist. PID 0 is the kernel/scheduler on macOS and never
// has a userspace cwd; PID -1 is similarly invalid. Either should fail.
func TestProcessCWD_NonexistentPID(t *testing.T) {
	// PID 0 is reserved (kernel/scheduler on macOS, swapper on Linux);
	// libproc and gopsutil both reject it.
	cwd, err := processCWD(0)
	if err == nil {
		t.Errorf("processCWD(0) unexpectedly succeeded: got cwd=%q", cwd)
	}
	if cwd != "" {
		t.Errorf("processCWD(0) returned a non-empty cwd on failure: %q (should always be empty when err != nil)", cwd)
	}
}
