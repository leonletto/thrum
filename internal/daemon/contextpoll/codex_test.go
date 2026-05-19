package contextpoll

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCodexTranscript writes lines (one JSON-encoded value per line) to a
// temp file inside dir and returns the path. Lines are written verbatim —
// pass a raw string to inject a deliberately malformed line.
func writeCodexTranscript(t *testing.T, dir string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, "rollout.jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// codexSessionMeta builds the canonical Codex first-line. The originator
// field is what Matches keys off, so tests can flip it to assert the
// negative case.
func codexSessionMeta(t *testing.T, originator string) string {
	t.Helper()
	type payload struct {
		ID         string `json:"id"`
		Originator string `json:"originator"`
		CliVersion string `json:"cli_version"`
	}
	type event struct {
		Timestamp string  `json:"timestamp"`
		Type      string  `json:"type"`
		Payload   payload `json:"payload"`
	}
	b, err := json.Marshal(event{
		Timestamp: "2026-02-24T18:31:41.717Z",
		Type:      "session_meta",
		Payload: payload{
			ID:         "019c90eb-4fe3-7cd1-bafd-448fdc05b4dd",
			Originator: originator,
			CliVersion: "0.104.0",
		},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

// codexTokenCount builds a populated token_count event with the given
// total_tokens + model_context_window.
func codexTokenCount(t *testing.T, totalTokens, contextWindow int) string {
	t.Helper()
	type totalUsage struct {
		TotalTokens int `json:"total_tokens"`
	}
	type info struct {
		TotalTokenUsage    totalUsage `json:"total_token_usage"`
		ModelContextWindow int        `json:"model_context_window"`
	}
	type payload struct {
		Type string `json:"type"`
		Info *info  `json:"info"`
	}
	type event struct {
		Type    string  `json:"type"`
		Payload payload `json:"payload"`
	}
	b, err := json.Marshal(event{
		Type: "event_msg",
		Payload: payload{
			Type: "token_count",
			Info: &info{
				TotalTokenUsage:    totalUsage{TotalTokens: totalTokens},
				ModelContextWindow: contextWindow,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

// codexTokenCountNullInfo builds the early-stage token_count event the
// Codex CLI emits before any tokens have been consumed: the payload
// carries only rate_limits, with info explicitly set to null. The parser
// must skip these.
func codexTokenCountNullInfo(t *testing.T) string {
	t.Helper()
	// Construct the JSON literally so info:null is preserved through the
	// encoding round-trip (a Go nil pointer marshals to `null`, but a
	// missing field would marshal as omitempty — preserve the explicit
	// null to mirror the wire format).
	return `{"timestamp":"2026-02-24T18:31:41.717Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":4.0}}}}`
}

func TestCodexParserV1_Version(t *testing.T) {
	if v := (CodexParserV1{}).Version(); v != "codex-v1" {
		t.Errorf("Version() = %q, want %q", v, "codex-v1")
	}
}

func TestCodexParserV1_NormalUsage(t *testing.T) {
	// 258400-token window * 50% = 129200 tokens used.
	path := writeCodexTranscript(t, t.TempDir(), []string{
		codexSessionMeta(t, "codex_cli_rs"),
		codexTokenCount(t, 129200, 258400),
	})
	usage, err := (CodexParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 50 {
		t.Errorf("UsedPercentage = %d, want 50", usage.UsedPercentage)
	}
	if usage.ParserVersion != "codex-v1" {
		t.Errorf("ParserVersion = %q, want codex-v1", usage.ParserVersion)
	}
	if usage.SourcePath != path {
		t.Errorf("SourcePath = %q, want %q", usage.SourcePath, path)
	}
	if usage.Approximate {
		t.Error("Approximate = true, want false (Codex total_tokens is cumulative + server-authoritative)")
	}
	if usage.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}
}

// TestCodexParserV1_SkipsInfoNull pins the wire-format quirk discovered in
// T9.1: the FIRST token_count event in a Codex session has info:null
// (rate-limits only). The parser must walk past those without reporting
// a 0% reading derived from a missing-info event.
func TestCodexParserV1_SkipsInfoNull(t *testing.T) {
	path := writeCodexTranscript(t, t.TempDir(), []string{
		codexSessionMeta(t, "codex_cli_rs"),
		codexTokenCountNullInfo(t),
		codexTokenCount(t, 64600, 258400), // 25% — the value Parse should return
	})
	usage, err := (CodexParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 25 {
		t.Errorf("UsedPercentage = %d, want 25 (info:null events skipped)", usage.UsedPercentage)
	}
}

// TestCodexParserV1_LastEventWins guards against an early-exit bug in
// future refactors. Multiple populated token_count events in the file:
// the LAST one must drive the result, mirroring the Claude parser
// semantic.
func TestCodexParserV1_LastEventWins(t *testing.T) {
	path := writeCodexTranscript(t, t.TempDir(), []string{
		codexSessionMeta(t, "codex_cli_rs"),
		codexTokenCount(t, 25840, 258400),  // 10% — earlier
		codexTokenCount(t, 180880, 258400), // 70% — later, winner
	})
	usage, err := (CodexParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 70 {
		t.Errorf("UsedPercentage = %d, want 70 (last event wins)", usage.UsedPercentage)
	}
}

func TestCodexParserV1_FileNotFound(t *testing.T) {
	_, err := (CodexParserV1{}).Parse(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err == nil {
		t.Fatal("Parse on missing file returned nil error, want non-nil")
	}
}

func TestCodexParserV1_MalformedJSON(t *testing.T) {
	// Malformed line precedes a valid token_count event; last-valid-line
	// semantics: parser skips the malformed line and returns the valid result.
	path := writeCodexTranscript(t, t.TempDir(), []string{
		codexSessionMeta(t, "codex_cli_rs"),
		`{this is not valid json`,
		codexTokenCount(t, 103360, 258400), // 40%
	})
	usage, err := (CodexParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 40 {
		t.Errorf("UsedPercentage = %d, want 40", usage.UsedPercentage)
	}
}

func TestCodexParserV1_NoPopulatedTokenCount(t *testing.T) {
	// File has content (session_meta + info:null event only) but no
	// populated token_count. Per the parser contract that should
	// surface as an error so the Poller logs + skips rather than
	// treating "unknown utilization" as 0% (which could mask a
	// threshold-crossing on the next poll).
	path := writeCodexTranscript(t, t.TempDir(), []string{
		codexSessionMeta(t, "codex_cli_rs"),
		codexTokenCountNullInfo(t),
	})
	if _, err := (CodexParserV1{}).Parse(path); err == nil {
		t.Fatal("Parse with only info:null token_count returned nil error, want non-nil")
	}
}

func TestCodexParserV1_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty fixture: %v", err)
	}
	usage, err := (CodexParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse on empty file: %v", err)
	}
	if usage.UsedPercentage != 0 {
		t.Errorf("UsedPercentage = %d, want 0", usage.UsedPercentage)
	}
}

func TestCodexParserV1_NearFull(t *testing.T) {
	// 320000 tokens against a 258400 window → should clamp to 100, not
	// overflow into 123 / report a meaningless value.
	path := writeCodexTranscript(t, t.TempDir(), []string{
		codexSessionMeta(t, "codex_cli_rs"),
		codexTokenCount(t, 320000, 258400),
	})
	usage, err := (CodexParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 100 {
		t.Errorf("UsedPercentage = %d, want 100 (clamped)", usage.UsedPercentage)
	}
}

// TestCodexParserV1_ZeroWindowReportsError defends the divide-by-zero
// guard in Parse. In practice Codex always emits a positive
// model_context_window on populated events, but a malformed transcript
// (corruption, mock data with the field zeroed, etc.) must not crash
// the daemon.
func TestCodexParserV1_ZeroWindowReportsError(t *testing.T) {
	path := writeCodexTranscript(t, t.TempDir(), []string{
		codexSessionMeta(t, "codex_cli_rs"),
		codexTokenCount(t, 10000, 0),
	})
	if _, err := (CodexParserV1{}).Parse(path); err == nil {
		t.Fatal("Parse with model_context_window=0 returned nil error, want non-nil")
	}
}

func TestCodexParserV1_Matches_ValidFile(t *testing.T) {
	path := writeCodexTranscript(t, t.TempDir(), []string{
		codexSessionMeta(t, "codex_cli_rs"),
	})
	if !(CodexParserV1{}).Matches(path) {
		t.Error("Matches returned false for a valid Codex JSONL first line")
	}
}

// TestCodexParserV1_Matches_WrongOriginator covers the Codex/Claude
// dispatch boundary: a file whose first line is type:"session_meta" but
// originator != "codex_cli_rs" must not match. Guards against a future
// runtime adopting "session_meta" without conflicting with Codex.
func TestCodexParserV1_Matches_WrongOriginator(t *testing.T) {
	path := writeCodexTranscript(t, t.TempDir(), []string{
		codexSessionMeta(t, "some_other_runtime"),
	})
	if (CodexParserV1{}).Matches(path) {
		t.Error("Matches returned true for session_meta with non-codex originator")
	}
}

func TestCodexParserV1_Matches_NotSessionMeta(t *testing.T) {
	// A Claude JSONL first-line shape — must not match the Codex parser.
	// Confirms first-line dispatch is robust against the other JSONL
	// formats the contextpoll package supports.
	path := writeCodexTranscript(t, t.TempDir(), []string{
		`{"type":"user","sessionId":"abc"}`,
	})
	if (CodexParserV1{}).Matches(path) {
		t.Error("Matches returned true for a Claude-format first line")
	}
}

func TestCodexParserV1_Matches_NonJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(path, []byte("not json at all\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if (CodexParserV1{}).Matches(path) {
		t.Error("Matches returned true for a plain-text file")
	}
}

func TestCodexParserV1_Matches_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty fixture: %v", err)
	}
	if (CodexParserV1{}).Matches(path) {
		t.Error("Matches returned true for an empty file")
	}
}

func TestCodexParserV1_Matches_MissingFile(t *testing.T) {
	if (CodexParserV1{}).Matches(filepath.Join(t.TempDir(), "nope.jsonl")) {
		t.Error("Matches returned true for a missing file")
	}
}
