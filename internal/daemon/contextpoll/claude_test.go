package contextpoll

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTranscript writes lines (one JSON-encoded value per line) to a temp
// file inside dir and returns the path. Lines are written verbatim — pass a
// raw string to inject a deliberately malformed line.
func writeTranscript(t *testing.T, dir string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, "transcript.jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// assistantEvent builds a JSON-encoded line matching the Claude assistant
// event shape: top-level type:"assistant" + nested message.{model, usage}.
func assistantEvent(t *testing.T, model string, input, cacheCreation, cacheRead int) string {
	t.Helper()
	type usage struct {
		InputTokens              int `json:"input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	}
	type message struct {
		Model string `json:"model"`
		Usage usage  `json:"usage"`
	}
	type event struct {
		Type    string  `json:"type"`
		Message message `json:"message"`
	}
	b, err := json.Marshal(event{
		Type: "assistant",
		Message: message{
			Model: model,
			Usage: usage{
				InputTokens:              input,
				CacheCreationInputTokens: cacheCreation,
				CacheReadInputTokens:     cacheRead,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

func TestClaudeParserV2x_Version(t *testing.T) {
	if v := (ClaudeParserV2x{}).Version(); v != "claude-v2x" {
		t.Errorf("Version() = %q, want %q", v, "claude-v2x")
	}
}

func TestClaudeParserV2x_NormalUsage(t *testing.T) {
	// 200K default window * 50% = 100K tokens.
	path := writeTranscript(t, t.TempDir(), []string{
		`{"type":"user"}`,
		assistantEvent(t, "claude-opus-4-7", 0, 0, 100000),
	})
	usage, err := (ClaudeParserV2x{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 50 {
		t.Errorf("UsedPercentage = %d, want 50", usage.UsedPercentage)
	}
	if usage.ParserVersion != "claude-v2x" {
		t.Errorf("ParserVersion = %q, want claude-v2x", usage.ParserVersion)
	}
	if usage.SourcePath != path {
		t.Errorf("SourcePath = %q, want %q", usage.SourcePath, path)
	}
	if usage.Approximate {
		t.Error("Approximate = true, want false (Claude reports running totals directly)")
	}
	if usage.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}
}

func TestClaudeParserV2x_FileNotFound(t *testing.T) {
	_, err := (ClaudeParserV2x{}).Parse(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err == nil {
		t.Fatal("Parse on missing file returned nil error, want non-nil")
	}
}

func TestClaudeParserV2x_MalformedJSON(t *testing.T) {
	// Malformed line precedes a valid assistant event; last-valid-line
	// semantics: parser skips the malformed line and returns the valid result.
	path := writeTranscript(t, t.TempDir(), []string{
		`{this is not valid json`,
		assistantEvent(t, "claude-sonnet-4-6", 1000, 19000, 60000), // 80K → 40%
	})
	usage, err := (ClaudeParserV2x{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 40 {
		t.Errorf("UsedPercentage = %d, want 40", usage.UsedPercentage)
	}
}

func TestClaudeParserV2x_NoValidAssistantEvents(t *testing.T) {
	// File has content but no parseable assistant event. Per the acceptance
	// criteria, that should surface as an error so the Poller logs + skips
	// instead of treating the unknown state as a threshold cross.
	path := writeTranscript(t, t.TempDir(), []string{
		`{garbage`,
		`also garbage`,
	})
	if _, err := (ClaudeParserV2x{}).Parse(path); err == nil {
		t.Fatal("Parse with all-malformed lines returned nil error, want non-nil")
	}
}

func TestClaudeParserV2x_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty fixture: %v", err)
	}
	usage, err := (ClaudeParserV2x{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse on empty file: %v", err)
	}
	if usage.UsedPercentage != 0 {
		t.Errorf("UsedPercentage = %d, want 0", usage.UsedPercentage)
	}
}

func TestClaudeParserV2x_NearFull(t *testing.T) {
	// 250K tokens against 200K default window → should clamp to 100, not
	// overflow into 125 / report a meaningless value.
	path := writeTranscript(t, t.TempDir(), []string{
		assistantEvent(t, "claude-opus-4-7", 100000, 100000, 50000),
	})
	usage, err := (ClaudeParserV2x{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 100 {
		t.Errorf("UsedPercentage = %d, want 100 (clamped)", usage.UsedPercentage)
	}
}

func TestClaudeParserV2x_LongContextModel(t *testing.T) {
	// Models tagged [1m] use the 1M window. 500K tokens against 1M = 50%.
	path := writeTranscript(t, t.TempDir(), []string{
		assistantEvent(t, "claude-opus-4-7[1m]", 0, 0, 500000),
	})
	usage, err := (ClaudeParserV2x{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 50 {
		t.Errorf("UsedPercentage = %d, want 50 (1M-context window)", usage.UsedPercentage)
	}
}

func TestClaudeParserV2x_LastEventWins(t *testing.T) {
	// Two assistant events in the file; the LATER one (lower in the file)
	// should drive the result — last-valid-line semantics.
	path := writeTranscript(t, t.TempDir(), []string{
		assistantEvent(t, "claude-opus-4-7", 0, 0, 20000),  // 10% — earlier
		assistantEvent(t, "claude-opus-4-7", 0, 0, 140000), // 70% — later, winner
	})
	usage, err := (ClaudeParserV2x{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 70 {
		t.Errorf("UsedPercentage = %d, want 70 (last event wins)", usage.UsedPercentage)
	}
}

func TestClaudeParserV2x_Matches_ValidFile(t *testing.T) {
	path := writeTranscript(t, t.TempDir(), []string{
		`{"type":"last-prompt","sessionId":"abc"}`,
	})
	if !(ClaudeParserV2x{}).Matches(path) {
		t.Error("Matches returned false for a valid Claude JSONL first line")
	}
}

func TestClaudeParserV2x_Matches_NonJSONFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(path, []byte("not json at all\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if (ClaudeParserV2x{}).Matches(path) {
		t.Error("Matches returned true for a plain-text file")
	}
}

func TestClaudeParserV2x_Matches_NoTypeField(t *testing.T) {
	// Valid JSON but missing the `type` field — must reject; this is the
	// signature that distinguishes Claude JSONL from arbitrary JSON-per-line
	// formats (e.g. some other tool's output).
	path := writeTranscript(t, t.TempDir(), []string{
		`{"sessionId":"abc","cwd":"/tmp"}`,
	})
	if (ClaudeParserV2x{}).Matches(path) {
		t.Error("Matches returned true for a JSON line without a type field")
	}
}

func TestClaudeParserV2x_Matches_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty fixture: %v", err)
	}
	if (ClaudeParserV2x{}).Matches(path) {
		t.Error("Matches returned true for an empty file")
	}
}

func TestClaudeParserV2x_Matches_MissingFile(t *testing.T) {
	if (ClaudeParserV2x{}).Matches(filepath.Join(t.TempDir(), "nope.jsonl")) {
		t.Error("Matches returned true for a missing file")
	}
}
