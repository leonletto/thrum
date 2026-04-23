package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestPeerAddRejectsTypeRepair verifies the CLI-layer guard: `thrum peer add
// --type repair` must fail with the canonical "not valid for peer add" error
// before any RPC round-trip. Daemon-side startPairingFn also rejects repair,
// but the CLI layer is the first line of defense — this test locks that in.
func TestPeerAddRejectsTypeRepair(t *testing.T) {
	cmd := peerCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"add", "--type", "repair"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error, got nil; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	msg := err.Error()
	if !strings.Contains(msg, "--type repair is not valid for 'peer add'") {
		t.Errorf("error message does not contain the canonical rejection text: %q", msg)
	}
	if !strings.Contains(msg, "thrum peer join --type repair") {
		t.Errorf("error should point the user at 'peer join --type repair': %q", msg)
	}
}

// TestPeerAddMissingTypePrintsHelpBlock confirms the CLI surfaces the full
// help block when --type is omitted (instead of a terser "required" message).
// This is the canonical xir.27 CLI-hint pattern.
func TestPeerAddMissingTypePrintsHelpBlock(t *testing.T) {
	cmd := peerCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"add"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error on missing --type")
	}
	msg := err.Error()
	for _, needle := range []string{
		"--type tailscale",
		"--type local",
		"--type network",
		"--type repair",
	} {
		if !strings.Contains(msg, needle) {
			t.Errorf("help block missing %q: %q", needle, msg)
		}
	}
	// Confirm a-sync is NOT mentioned (scope correction).
	if strings.Contains(msg, "a-sync") {
		t.Errorf("help block unexpectedly mentions a-sync: %q", msg)
	}
}
