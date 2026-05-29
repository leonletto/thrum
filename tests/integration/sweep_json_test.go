package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestErrorContextSweep_JSONMode verifies the --json detection seam
// (thrum-sdzk): the sweep emits one structured JSON object per flagged agent,
// which the daemon auto-remediation handler consumes instead of re-parsing
// JSONL transcripts in Go. Drives the script against a fixture fleet with a
// fake tmux that returns an API-error pane line, then asserts the JSON record
// carries the agent, target, error, and flagged_reason.
func TestErrorContextSweep_JSONMode(t *testing.T) {
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

	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	idDir := filepath.Join(tmp, "identities")
	binDir := filepath.Join(tmp, "bin")
	for _, d := range []string{home, idDir, binDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	identity := `{"agent":{"Name":"fake_agent","Role":"tester","Module":"x"},` +
		`"tmux_session":"fakesess:0.0","worktree":"` + filepath.Join(tmp, "wt") + `",` +
		`"updated_at":"2026-05-22T15:00:00Z","agent_status":"working"}`
	if err := os.WriteFile(filepath.Join(idDir, "fake.json"), []byte(identity), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	fakeTmux := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  list-sessions) echo fakesess ;;\n" +
		"  capture-pane) printf '%s\\n' '  \xe2\x8e\xbf  API Error 529 overloaded' ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(fakeTmux), 0o700); err != nil { // #nosec G306 -- test shim must be executable
		t.Fatalf("write fake tmux: %v", err)
	}

	cmd := exec.Command("bash", scriptPath, "--json", "--no-nudge") // #nosec G204 -- test-controlled script + args
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"THRUM_SWEEP_IDENTITY_GLOBS="+filepath.Join(idDir, "*.json"),
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("sweep --json failed: %v\noutput:\n%s", runErr, out)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 flagged JSON record, got %d:\n%s", len(lines), out)
	}

	var rec struct {
		AgentID       string `json:"agent_id"`
		TmuxTarget    string `json:"tmux_target"`
		APIErrors     string `json:"api_errors"`
		FlaggedReason string `json:"flagged_reason"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("record is not valid JSON: %v\nline: %s", err, lines[0])
	}
	if rec.AgentID != "fake_agent" {
		t.Errorf("agent_id = %q, want fake_agent", rec.AgentID)
	}
	if rec.TmuxTarget != "fakesess:0.0" {
		t.Errorf("tmux_target = %q, want fakesess:0.0", rec.TmuxTarget)
	}
	if !strings.Contains(rec.APIErrors, "API Error") {
		t.Errorf("api_errors = %q, want it to carry the API Error text", rec.APIErrors)
	}
	if rec.FlaggedReason != "api_error" {
		t.Errorf("flagged_reason = %q, want api_error", rec.FlaggedReason)
	}
}
