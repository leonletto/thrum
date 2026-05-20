// Package contextpoll Codex parser (CR.9 / thrum-6qmf.1.27).
//
// Codex transcript discovery summary (full discovery notes preserved
// inline below; original investigation was T9.1 / thrum-6qmf.1.26):
//
//   Path:    $HOME/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<ISO8601>-<ULID>.jsonl
//   Format:  JSONL.
//   Anchor:  first line is type=="session_meta" with
//            payload.originator=="codex_cli_rs"
//   Event:   type=="event_msg" + payload.type=="token_count"
//            + payload.info != null  (early-stage events carry only
//            rate_limits with info:null — skipped)
//   Usage:   info.total_token_usage.total_tokens * 100
//            / info.model_context_window
//   Approximate: FALSE — cumulative + server-authoritative.
//
// Implementation parallels ClaudeParserV2x: bufio.Scanner walk; last
// populated token_count event wins; malformed JSON lines silently
// skipped (the Codex writer flushes a partial line at session end on
// some abort paths). Schema-tolerant — extra fields in payload.info or
// payload.rate_limits are ignored.

package contextpoll

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// CodexParserV1 parses Codex (codex_cli_rs) JSONL session transcripts.
//
// "V1" matches Codex's session-meta wire format as observed in
// codex_cli_rs 0.104.x rollout files. The dispatch tag follows the
// V1/V2x naming convention used by Claude: a major-version bump on the
// wire shape would ship as CodexParserV2x without disturbing this one.
type CodexParserV1 struct{}

// Version returns the parser version tag used for log attribution.
func (CodexParserV1) Version() string { return "codex-v1" }

// Matches returns true when the first line of path is a Codex
// session_meta event. The originator field is the unambiguous anchor —
// Codex is the only runtime currently writing the
// "codex_cli_rs" originator string.
//
// Returns false for empty files, unreadable files, and files whose
// first line is not a valid Codex session_meta event.
func (CodexParserV1) Matches(path string) bool {
	f, err := os.Open(path) // #nosec G304 -- path is an absolute JSONL path resolved by restart.FindSessionJSONL / FindLatestJSONLForCwd
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	// Codex session_meta carries base_instructions prose that runs
	// many KB; raise the per-line cap well past bufio's 64KB default.
	scanner.Buffer(make([]byte, 1<<16), 16<<20)
	if !scanner.Scan() {
		return false
	}
	var probe struct {
		Type    string `json:"type"`
		Payload struct {
			Originator string `json:"originator"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &probe); err != nil {
		return false
	}
	return probe.Type == "session_meta" && probe.Payload.Originator == "codex_cli_rs"
}

// codexEventMsg is the minimal projection of a Codex JSONL line needed
// to compute context utilization. Other fields on the wire
// (timestamp, rate_limits, payload.info.last_token_usage, ...) are
// deliberately ignored — the parser is schema-tolerant by design.
//
// Info is a pointer so a token_count event with `info: null`
// (rate-limits-only, emitted before the first model call) decodes as
// nil rather than a zero-valued struct. The parser dispatch keys off
// Info != nil specifically so those early events don't trip a 0%
// false reading.
type codexEventMsg struct {
	Type    string `json:"type"`
	Payload struct {
		Type string               `json:"type"`
		Info *codexTokenCountInfo `json:"info"`
	} `json:"payload"`
}

type codexTokenCountInfo struct {
	TotalTokenUsage    codexTokenUsage `json:"total_token_usage"`
	ModelContextWindow int             `json:"model_context_window"`
}

type codexTokenUsage struct {
	TotalTokens int `json:"total_tokens"`
}

// Parse reads path (a Codex session JSONL) and returns the context
// utilization derived from the last populated token_count event.
//
// Error contract:
//   - File-not-found and other open errors return a non-nil error.
//   - Empty file (zero bytes / no lines) returns UsedPercentage 0 and
//     no error — a fresh session that has not produced an event yet.
//   - Malformed JSON lines are skipped silently.
//   - A non-nil error is returned if the file is non-empty but no
//     populated token_count event was found.
//   - A populated event with model_context_window == 0 returns an
//     error rather than dividing by zero. (Defensive; in practice
//     model_context_window is always positive on real Codex output.)
//
// UsedPercentage = total_token_usage.total_tokens * 100
//
//	/ model_context_window
//
// clamped to [0, 100]. Approximate is always false — total_tokens is
// the cumulative server-authoritative count, same semantic as Claude.
func (CodexParserV1) Parse(path string) (ContextUsage, error) {
	f, err := os.Open(path) // #nosec G304 -- path is an absolute JSONL path resolved by restart.FindSessionJSONL / FindLatestJSONLForCwd
	if err != nil {
		return ContextUsage{}, fmt.Errorf("contextpoll: open codex transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<16), 16<<20)

	var (
		lastInfo    *codexTokenCountInfo
		sawAnyLines bool
	)
	for scanner.Scan() {
		sawAnyLines = true
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev codexEventMsg
		if err := json.Unmarshal(line, &ev); err != nil {
			// Malformed line — skip per last-valid-line semantics.
			continue
		}
		if ev.Type != "event_msg" {
			continue
		}
		if ev.Payload.Type != "token_count" {
			continue
		}
		if ev.Payload.Info == nil {
			// Rate-limits-only event; carries no token totals. Skip.
			continue
		}
		lastInfo = ev.Payload.Info
	}
	if err := scanner.Err(); err != nil {
		return ContextUsage{}, fmt.Errorf("contextpoll: scan codex transcript: %w", err)
	}

	// Empty file — no lines at all. Treat as fresh session.
	if !sawAnyLines {
		return ContextUsage{
			UsedPercentage: 0,
			ParserVersion:  "codex-v1",
			SourcePath:     path,
			Timestamp:      time.Now(),
			Approximate:    false,
		}, nil
	}

	// File had content but no populated token_count event. Surface as
	// an error so the caller can log + skip without treating it as a
	// threshold cross.
	if lastInfo == nil {
		return ContextUsage{}, fmt.Errorf("contextpoll: no populated token_count events in %s", path)
	}

	window := lastInfo.ModelContextWindow
	if window <= 0 {
		return ContextUsage{}, fmt.Errorf("contextpoll: codex token_count event has non-positive model_context_window in %s", path)
	}
	pct := lastInfo.TotalTokenUsage.TotalTokens * 100 / window
	pct = max(0, min(100, pct))

	return ContextUsage{
		UsedPercentage: pct,
		ParserVersion:  "codex-v1",
		SourcePath:     path,
		Timestamp:      time.Now(),
		Approximate:    false,
	}, nil
}

// Full discovery notes (preserved from T9.1 investigation; original
// commit e383276f46):
//
// Path layout
// -----------
// Codex (codex_cli_rs, version 0.104.0+) writes one JSONL transcript
// per session at:
//
//   $HOME/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<ISO8601>-<ULID>.jsonl
//
// Example:
//   ~/.codex/sessions/2026/02/24/rollout-2026-02-24T10-31-06-019c90eb-4fe3-7cd1-bafd-448fdc05b4dd.jsonl
//
// The date segment uses the session-start instant in local time per
// the observed files (no TZ probing necessary at the parser level —
// we read the path as opaque once resolved). The CLI also writes an
// aggregated index at $HOME/.codex/session_index.jsonl + an archive
// at $HOME/.codex/archived_sessions/, neither of which is needed for
// parsing a single session's current context usage.
//
// Token-usage event wire shape (the type Parse projects against)
// --------------------------------------------------------------
//   {"timestamp":"...","type":"event_msg","payload":{
//       "type":"token_count",
//       "info":{
//           "total_token_usage":{
//               "input_tokens":10149,
//               "cached_input_tokens":8064,
//               "output_tokens":31,
//               "reasoning_output_tokens":0,
//               "total_tokens":10180
//           },
//           "last_token_usage":{...same shape...},
//           "model_context_window":258400
//       },
//       "rate_limits":{...}
//   }}
//
// The first token_count event in a session has `info: null`
// (rate-limits only, before any tokens have been consumed). Parsers
// must skip those lines and walk to the LAST token_count event with
// info != null. Verified against rollout-2026-02-24T10-31-06: of 1246
// token_count events, the first has info:null and the rest carry the
// full payload.
//
// model_context_window is a property of the model the session is
// using; it is emitted on every populated token_count event, so the
// parser does not need a model-name → context-window lookup table.
// Field is integer tokens (e.g. 258400 for GPT-5 on the codex plan).
