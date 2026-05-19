// Codex transcript discovery (CR.9 / thrum-6qmf.1.26)
//
// Status: PARSABLE. Path + token-usage format both identified during the
// T9.1 investigation. Implementation (T9.2 / thrum-6qmf.1.27) can proceed
// using the same structural pattern as ClaudeParserV2x.
//
// Path layout
// -----------
// Codex (codex_cli_rs, version 0.104.0+) writes one JSONL transcript per
// session at:
//
//   $HOME/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<ISO8601>-<ULID>.jsonl
//
// Example:
//   ~/.codex/sessions/2026/02/24/rollout-2026-02-24T10-31-06-019c90eb-4fe3-7cd1-bafd-448fdc05b4dd.jsonl
//
// The date segment uses the session-start instant in local time per the
// observed files (no TZ probing necessary at the parser level — we read
// the path as opaque once resolved). The CLI also writes an aggregated
// index at $HOME/.codex/session_index.jsonl + an archive at
// $HOME/.codex/archived_sessions/, neither of which is needed for parsing
// a single session's current context usage.
//
// First-line anchor (Parser.Matches)
// ----------------------------------
// First line is always a session_meta event:
//
//   {"timestamp":"...","type":"session_meta","payload":{
//       "id":"<ULID>","originator":"codex_cli_rs","cli_version":"...",
//       "cwd":"...","model_provider":"openai","base_instructions":{...}, ...}}
//
// Robust anchor: top-level type == "session_meta" AND
// payload.originator == "codex_cli_rs". Codex is the only runtime currently
// using that originator string, so the dispatch is unambiguous.
//
// Token-usage event shape (Parser.Parse target)
// ---------------------------------------------
// Each model call emits a token_count event AFTER its corresponding
// agent_message. Shape (last-event-wins):
//
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
// The first token_count event in a session has `info: null` (rate-limits
// only, before any tokens have been consumed). Parsers must skip those
// lines and walk to the LAST token_count event with `info != null`.
// Verified against rollout-2026-02-24T10-31-06: of 1246 token_count
// events, the first has info:null and the rest carry the full payload.
//
// UsedPercentage computation
// --------------------------
// usage_pct = total_token_usage.total_tokens * 100 / model_context_window
//
// model_context_window is a property of the model the session is using; it
// is emitted on every populated token_count event, so the parser does not
// need a model-name → context-window lookup table. Field is integer
// tokens (e.g. 258400 for GPT-5 on the codex plan).
//
// Approximate flag: FALSE. total_token_usage.total_tokens is the
// authoritative cumulative cumulative count from the server response,
// matching the Claude parser's semantic (cumulative input + cache_create
// + cache_read). No reconstruction needed.
//
// Implementation budget (T9.2)
// ----------------------------
// Mirror ClaudeParserV2x:
//   - bufio.Scanner with 16<<20 max-buffer (Codex events are bounded but
//     base_instructions on session_meta can be > 64KB).
//   - Single-pass scan, keep track of the LAST event where
//     type=="event_msg" AND payload.type=="token_count" AND
//     payload.info != nil.
//   - Compute usage_pct + clamp to [0,100].
//   - Return ContextUsage{UsedPercentage, ParserVersion:"codex-v1",
//       SourcePath, Timestamp:time.Now().UTC(), Approximate:false}.
//
// No dependency on codex-rs source — wire format is stable across
// 0.104.x per the rollout files inspected. Should a future major bump
// break the shape, the dispatch (NewCodexParserV2 / CodexParserV1)
// follows the same V1/V2x naming convention used by Claude.
//
// Test fixtures (T9.3)
// --------------------
// Lift a redacted 50-line slice of a real rollout file into the testdata
// directory; the session_meta line + a sequence of token_count events
// covering: (a) info:null first event, (b) populated event, (c) growing
// context, (d) at-cap event. Mirror the ClaudeParserV2x test scaffolding
// for: NormalUsage, NearFull, EmptyFile, MalformedJSON, NoValidEvents,
// LastEventWins. Approximate flag asserted false in NormalUsage.

package contextpoll
