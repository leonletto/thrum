package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	testBinaryOnce sync.Once
	testBinaryPath string
	testBinaryErr  error
)

// buildTestBinary compiles ./cmd/thrum into a temp file and returns the path.
// Cached per-process; repeated calls reuse the same binary.
//
// Intentionally uses bare `go build` rather than `make dev` — L4 tests build
// a fresh throwaway binary for isolation, not the signed `./bin/thrum`.
// Tests that require macOS signing should skip and run manually.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	testBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "thrum-test-*")
		if err != nil {
			testBinaryErr = err
			return
		}
		testBinaryPath = filepath.Join(dir, "thrum")
		cmd := exec.Command("go", "build", "-o", testBinaryPath, ".")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			testBinaryErr = err
		}
	})
	if testBinaryErr != nil {
		t.Fatalf("build test binary: %v", testBinaryErr)
	}
	return testBinaryPath
}

// captureOut attaches stdout/stderr capture buffers to cmd and returns them.
// Buffers are filled after cmd.Run() completes.
func captureOut(cmd *exec.Cmd) (stdout, stderr *bytes.Buffer) {
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return
}
