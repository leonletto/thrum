package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestLegacyHintTipStillEmits is the L5 backwards-compat smoke test: commands
// wired to the legacy flat-map (cli.LegacyHint) — inbox, quickstart, overview,
// etc. — must still emit a random "Tip:" line in text mode. Protects the
// fallback path from regression as the new hint mechanism evolves.
//
// Uses 'overview' because it reaches the cli.LegacyHint("overview", ...) Call
// at main.go line ~4633 without requiring a live daemon session.
func TestLegacyHintTipStillEmits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short")
	}
	bin := buildTestBinary(t)

	// The 'overview' command calls cli.LegacyHint which emits on stdout
	// (legacy behavior — the new mechanism emits on stderr). We check the
	// combined output and require a "Tip:" line somewhere.
	cmd := exec.Command(bin, "overview")
	outBuf, errBuf := captureOut(cmd)
	_ = cmd.Run()

	combined := outBuf.String() + errBuf.String()
	if !strings.Contains(combined, "Tip:") {
		t.Errorf("expected legacy 'Tip:' line in output of 'thrum overview', got:\nstdout:\n%s\nstderr:\n%s",
			outBuf.String(), errBuf.String())
	}
}
