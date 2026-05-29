package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestErrorContextSweep_NoNudge_SuppressesSendKeys is the thrum-d007.2
// daemon-report-only guard. The error-and-context sweep script auto-nudges
// ("continue") into any pane showing an API error. The daemon-hosted built-in
// passes --no-nudge so a detection sweep performs ZERO pane writes. This drives
// the script against a fixture fleet with a fake tmux that logs every
// invocation, and asserts:
//   - WITHOUT --no-nudge: send-keys IS issued (positive control — the nudge
//     path is actually reachable in the fixture).
//   - WITH --no-nudge: send-keys is NEVER issued.
func TestErrorContextSweep_NoNudge_SuppressesSendKeys(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available (script dependency)")
	}

	scriptPath, err := filepath.Abs("../../scripts/error-and-context-agent-sweep.sh")
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("sweep script missing: %v", err)
	}

	tmp := t.TempDir()
	home := filepath.Join(tmp, "home") // empty HOME → no Claude transcript → non-claude pane-scan path
	idDir := filepath.Join(tmp, "identities")
	binDir := filepath.Join(tmp, "bin")
	for _, d := range []string{home, idDir, binDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Fixture identity: a non-claude agent on an alive tmux session, whose
	// worktree has no Claude transcript dir under our temp HOME.
	identity := `{"agent":{"Name":"fake_agent","Role":"tester","Module":"x"},` +
		`"tmux_session":"fakesess:0.0","worktree":"` + filepath.Join(tmp, "wt") + `",` +
		`"updated_at":"2026-05-22T15:00:00Z","agent_status":"working"}`
	if err := os.WriteFile(filepath.Join(idDir, "fake.json"), []byte(identity), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	// Fake tmux: report the session alive, return a pane with an API-error
	// line (the ⎿-anchored form the script greps), and log send-keys calls.
	nudgeLog := filepath.Join(tmp, "tmux-sendkeys.log")
	fakeTmux := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  list-sessions) echo fakesess ;;\n" +
		"  capture-pane) printf '%s\\n' '  \xe2\x8e\xbf  API Error 529 overloaded' ;;\n" +
		"  send-keys) echo \"send-keys $*\" >> \"$NUDGE_LOG\" ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(fakeTmux), 0o700); err != nil { // #nosec G306 -- test shim must be executable
		t.Fatalf("write fake tmux: %v", err)
	}

	run := func(extraArgs ...string) {
		_ = os.Remove(nudgeLog)
		args := append([]string{scriptPath}, extraArgs...)
		cmd := exec.Command("bash", args...) // #nosec G204 -- test-controlled script + args
		cmd.Env = append(os.Environ(),
			"HOME="+home,
			"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"THRUM_SWEEP_IDENTITY_GLOBS="+filepath.Join(idDir, "*.json"),
			"NUDGE_LOG="+nudgeLog,
		)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("sweep run %v failed: %v\noutput:\n%s", extraArgs, runErr, out)
		}
	}

	sentKeys := func() bool {
		data, err := os.ReadFile(nudgeLog)
		if err != nil {
			return false // no log file → no send-keys
		}
		return strings.Contains(string(data), "send-keys")
	}

	// Positive control: default behavior nudges, proving the path is reachable.
	run()
	if !sentKeys() {
		t.Fatal("positive control failed: default run did not send-keys; nudge path not exercised by fixture")
	}

	// The contract: --no-nudge performs zero pane writes.
	run("--no-nudge")
	if sentKeys() {
		t.Fatal("--no-nudge must suppress send-keys, but the daemon-mode run nudged a pane")
	}

	// Alias must behave identically.
	run("--report-only")
	if sentKeys() {
		t.Fatal("--report-only must suppress send-keys")
	}
}
