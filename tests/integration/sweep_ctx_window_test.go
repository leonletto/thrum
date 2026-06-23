package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestErrorContextSweep_CtxWindowByModel is the thrum-4pd1 regression guard.
//
// The sweep derives ctx_used as used_tokens/window. The window denominator is
// picked per model: the Opus 4 1m-context fleet uses a 1,000,000-token window,
// everything else falls back to a conservative 200,000. A hardcoded
// `claude-opus-4-7*` match silently dropped newer Opus 4.8 agents to the 200k
// default, dividing their 1M-window token counts by 200k and inflating ctx_used
// by exactly 5x (the 216%/43%, 117%/23% etc. ratios observed in production).
//
// The usage field shape is IDENTICAL across 4.7 and 4.8 (input_tokens +
// cache_creation_input_tokens + cache_read_input_tokens); only the model string
// and thus the window match differ. This test pins BOTH shapes: it drives the
// script against a fixture transcript carrying a known 900k-token usage and
// asserts the computed percentage is 90.0% against the 1M window for each of
// claude-opus-4-7 and claude-opus-4-8 — not the 450% the old code produced for
// 4.8. The non-opus fallback case pins the conservative 200k default stays.
func TestErrorContextSweep_CtxWindowByModel(t *testing.T) {
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

	cases := []struct {
		name string
		// model is written verbatim into the transcript's assistant record.
		model string
		// wantCtxUsed is the exact ctx_used string the sweep must emit. The
		// format is "%.1f%% (%dk/%dk %s)" with the model's claude- prefix
		// stripped — see the awk/sed at error-and-context-agent-sweep.sh.
		wantCtxUsed string
	}{
		{
			name:        "opus-4-7 uses 1M window",
			model:       "claude-opus-4-7",
			wantCtxUsed: "90.0% (900k/1000k opus-4-7)",
		},
		{
			name:        "opus-4-8 uses 1M window (thrum-4pd1 regression)",
			model:       "claude-opus-4-8",
			wantCtxUsed: "90.0% (900k/1000k opus-4-8)",
		},
		{
			// Pins the comment's claim that the glob matches the [1m] suffix
			// form should Claude ever record it verbatim in the JSONL model
			// field. claude-opus-4-* matches "claude-opus-4-8[1m]" (the * eats
			// the bracketed suffix); the [1m] is literal in the sed replacement.
			name:        "opus-4-8 1m-suffix form uses 1M window",
			model:       "claude-opus-4-8[1m]",
			wantCtxUsed: "90.0% (900k/1000k opus-4-8[1m])",
		},
		{
			name:        "unknown model keeps conservative 200k default",
			model:       "claude-sonnet-9-9",
			wantCtxUsed: "450.0% (900k/200k sonnet-9-9)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			home := filepath.Join(tmp, "home")
			idDir := filepath.Join(tmp, "identities")
			binDir := filepath.Join(tmp, "bin")
			worktree := filepath.Join(tmp, "wt")
			for _, d := range []string{home, idDir, binDir, worktree} {
				if err := os.MkdirAll(d, 0o750); err != nil {
					t.Fatalf("mkdir %s: %v", d, err)
				}
			}

			// Transcript dir: the script encodes the worktree path by replacing
			// every '/' and '.' with '-' under $HOME/.claude/projects/.
			encoded := strings.NewReplacer("/", "-", ".", "-").Replace(worktree)
			transcriptDir := filepath.Join(home, ".claude", "projects", encoded)
			if err := os.MkdirAll(transcriptDir, 0o750); err != nil {
				t.Fatalf("mkdir transcript dir: %v", err)
			}

			// Realistic multi-turn transcript: a user turn (provides the
			// birth timestamp the JSONL-pick logic sorts on), an early
			// assistant turn with a SMALL usage, and the latest assistant
			// turn with the 900k usage we assert on. The sweep reads the LAST
			// usage record — proving it reports the current turn, not a sum
			// across turns. 900k = input(100k) + cache_creation(300k) +
			// cache_read(500k); the three fields are summed identically for
			// both model versions.
			jsonl := strings.Join([]string{
				`{"timestamp":"2026-05-29T06:00:00.000Z","type":"user","message":{"role":"user","content":"hi"}}`,
				`{"timestamp":"2026-05-29T06:10:00.000Z","type":"assistant","message":{"model":"` + tc.model + `","stop_reason":"end_turn","usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":5}}}`,
				`{"timestamp":"2026-05-29T06:30:00.000Z","type":"assistant","message":{"model":"` + tc.model + `","stop_reason":"end_turn","usage":{"input_tokens":100000,"cache_creation_input_tokens":300000,"cache_read_input_tokens":500000,"output_tokens":50}}}`,
				"",
			}, "\n")
			if err := os.WriteFile(filepath.Join(transcriptDir, "session.jsonl"), []byte(jsonl), 0o600); err != nil {
				t.Fatalf("write transcript: %v", err)
			}

			identity := `{"agent":{"Name":"fake_agent","Role":"tester","Module":"x"},` +
				`"tmux_session":"fakesess:0.0","worktree":"` + worktree + `",` +
				`"updated_at":"2026-05-29T06:30:00Z","agent_status":"idle"}`
			if err := os.WriteFile(filepath.Join(idDir, "fake.json"), []byte(identity), 0o600); err != nil {
				t.Fatalf("write identity: %v", err)
			}

			// Fake tmux: alive session, recent window_activity (silence ~0 so
			// no stuck flag), empty pane (no API error so the only flag is ctx).
			fakeTmux := "#!/bin/sh\n" +
				"case \"$1\" in\n" +
				"  list-sessions) echo fakesess ;;\n" +
				"  display-message) date -u +%s ;;\n" +
				"  capture-pane) printf '' ;;\n" +
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
				AgentID  string `json:"agent_id"`
				CtxUsed  string `json:"ctx_used"`
				APIError string `json:"api_errors"`
			}
			if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
				t.Fatalf("record is not valid JSON: %v\nline: %s", err, lines[0])
			}

			if rec.CtxUsed != tc.wantCtxUsed {
				t.Errorf("ctx_used = %q, want %q", rec.CtxUsed, tc.wantCtxUsed)
			}

			// Explicit bug-signature guard for the Opus 4.8 path: the old code
			// divided the 1M-window token count by 200k. If 4.8 ever falls back
			// to the 200k default again, the percentage balloons past 100% —
			// catch that distinctly from a generic string mismatch.
			if tc.model == "claude-opus-4-8" && strings.Contains(rec.CtxUsed, "200k") {
				t.Errorf("thrum-4pd1 regression: opus-4-8 fell through to the 200k default window (ctx_used=%q)", rec.CtxUsed)
			}
		})
	}
}
