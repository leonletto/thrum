package contextpoll

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// claudeDefaultContextWindow is the standard Claude-family context window
// (Opus / Sonnet / Haiku 4.x). Long-context variants (1M) are detected via
// the [1m] suffix on the model name in the transcript.
//
// Verified 2026-05-18 against the Anthropic model docs and the locally
// observed claude-opus-4-7 transcript shape. Update the lookup in
// claudeContextWindow when new model families ship; the simple
// prefix/contains check keeps the map maintenance-light.
const claudeDefaultContextWindow = 200000

// claudeContextWindow returns the context-window size for a Claude model.
// Returns claudeDefaultContextWindow (200000) for unknown models — a safe
// over-estimate of utilization rather than an under-estimate, since
// over-eager warn nudges are recoverable while missed thresholds are not.
func claudeContextWindow(model string) int {
	// 1M long-context variants are tagged by Claude Code with a "[1m]" suffix
	// on the model id (e.g. "claude-opus-4-7[1m]"). Bare model ids default to
	// the standard 200K window.
	if strings.Contains(model, "[1m]") {
		return 1_000_000
	}
	return claudeDefaultContextWindow
}

// ClaudeParserV2x parses Claude Code JSONL session transcripts.
//
// Version "v2x" tracks the Claude Code 2.x JSONL surface: per-event JSON
// objects with top-level `type` (e.g. "assistant", "user", "system",
// "permission-mode", ...) and assistant events carrying a nested `message`
// object with `model` and `usage` fields.
//
// Last-valid-line semantics: Parse scans the whole file and returns the
// utilization derived from the LAST assistant event encountered. Malformed
// JSON lines are skipped silently — Claude Code occasionally flushes a
// partially-written line during the next-event handoff, and treating those
// as fatal would make every active transcript unreadable.
type ClaudeParserV2x struct{}

// Version returns the parser version tag used for log attribution.
func (ClaudeParserV2x) Version() string { return "claude-v2x" }

// Matches returns true when the first line of path is a JSON object carrying
// a non-empty `type` field — the signature of a Claude Code JSONL.
// Returns false for empty files, unreadable files, and files whose first line
// is not valid JSON with a `type` field.
func (ClaudeParserV2x) Matches(path string) bool {
	f, err := os.Open(path) // #nosec G304 -- path is an absolute JSONL path resolved by restart.FindSessionJSONL / FindLatestJSONLForCwd
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	// Claude JSONL lines can carry large thinking / tool-result payloads;
	// raise the per-line cap well past bufio's 64KB default so we never
	// misclassify a valid transcript as malformed because the first line
	// happened to be long.
	scanner.Buffer(make([]byte, 1<<16), 16<<20)
	if !scanner.Scan() {
		return false
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &probe); err != nil {
		return false
	}
	return probe.Type != ""
}

// claudeAssistantEvent is the minimal projection of a Claude JSONL line
// needed to compute context utilization. Other fields on the wire (parentUuid,
// timestamp, cwd, gitBranch, ...) are deliberately ignored — the parser is
// schema-tolerant by design.
type claudeAssistantEvent struct {
	Type    string `json:"type"`
	Message struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Parse reads path (a Claude Code session JSONL) and returns the context
// utilization derived from the last assistant event in the file.
//
// Error contract:
//   - File-not-found and other open errors return a non-nil error.
//   - Empty file (zero bytes / no lines) returns UsedPercentage 0 and no error
//     — a fresh session that hasn't produced an assistant turn yet.
//   - Malformed JSON lines are skipped. A non-nil error is returned only if
//     the file is non-empty AND no parseable assistant event was found.
//
// UsedPercentage is computed as
//
//	(input_tokens + cache_creation_input_tokens + cache_read_input_tokens) * 100
//	  / context_window
//
// and clamped to [0, 100]. Approximate is always false: Claude reports its
// own running cumulative-token counts directly, no reconstruction needed.
func (ClaudeParserV2x) Parse(path string) (ContextUsage, error) {
	f, err := os.Open(path) // #nosec G304 -- path is an absolute JSONL path resolved by restart.FindSessionJSONL / FindLatestJSONLForCwd
	if err != nil {
		return ContextUsage{}, fmt.Errorf("contextpoll: open claude transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<16), 16<<20)

	var (
		lastUsage   claudeAssistantEvent
		haveUsage   bool
		sawAnyLines bool
	)
	for scanner.Scan() {
		sawAnyLines = true
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev claudeAssistantEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Malformed line — skip per last-valid-line semantics.
			continue
		}
		if ev.Type != "assistant" {
			continue
		}
		lastUsage = ev
		haveUsage = true
	}
	if err := scanner.Err(); err != nil {
		return ContextUsage{}, fmt.Errorf("contextpoll: scan claude transcript: %w", err)
	}

	// Empty file — no lines at all. Treat as fresh session.
	if !sawAnyLines {
		return ContextUsage{
			UsedPercentage: 0,
			ParserVersion:  "claude-v2x",
			SourcePath:     path,
			Timestamp:      time.Now(),
			Approximate:    false,
		}, nil
	}

	// File had content but no parseable assistant events. Surface as error
	// so the caller can log + skip without treating it as a threshold cross.
	if !haveUsage {
		return ContextUsage{}, fmt.Errorf("contextpoll: no assistant events found in %s", path)
	}

	sum := lastUsage.Message.Usage.InputTokens +
		lastUsage.Message.Usage.CacheCreationInputTokens +
		lastUsage.Message.Usage.CacheReadInputTokens
	window := claudeContextWindow(lastUsage.Message.Model)
	if window <= 0 {
		window = claudeDefaultContextWindow
	}
	pct := sum * 100 / window
	pct = max(0, min(100, pct))

	return ContextUsage{
		UsedPercentage: pct,
		ParserVersion:  "claude-v2x",
		SourcePath:     path,
		Timestamp:      time.Now(),
		Approximate:    false,
	}, nil
}
