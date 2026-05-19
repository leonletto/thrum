// OpenCode transcript discovery (CR.8 / thrum-6qmf.1.23)
//
// Status: PATH FOUND, FORMAT FULLY READABLE, BUT ARCHITECTURAL FOLLOW-UP
// REQUIRED. OpenCode does NOT persist transcripts as JSONL files. It uses
// a per-user SQLite database with a JSON-blob column. The current
// contextpoll.Parser interface — `Matches(path string) bool` +
// `Parse(path string) (ContextUsage, error)` — assumes the parser opens
// `path` as a regular file and reads it linearly. SQLite parsing breaks
// that contract.
//
// Two viable resolutions:
//
//   A) **Treat the DB file as the "path" and gain a sql.DB dependency
//      inside the parser.** Smallest change to the wiring (the
//      enrollment loop just resolves the OpenCode DB path instead of a
//      JSONL path). Cost: contextpoll's existing zero-external-dep
//      character is broken — the package picks up `database/sql` +
//      mattn/go-sqlite3 transitively. Concurrent-access correctness is
//      another concern: opencode itself has the DB open with WAL mode
//      (opencode.db-shm + opencode.db-wal present); a daemon reader
//      that mishandles the lock could trip OpenCode. Mitigated by
//      `?mode=ro&_journal=WAL` URI but the failure mode is subtle.
//
//   B) **Skip OpenCode for v0.11 and fall back to manual
//      `/thrum:restart` discipline.** Document the path + format here
//      (so a future implementer can pick this up cheaply) and register
//      no parser. ContextPoller silently ignores OpenCode-runtime
//      agents because Matches() never matches their TranscriptPath
//      (the wiring would just pass empty TranscriptPath for them — the
//      Poller's empty-path skip handles it).
//
// This file's existence + the `// TODO(CR.8-fallback)` marker at the end
// signals to T8.2 / thrum-6qmf.1.24 that the implementer must choose
// between A and B before writing the parser body. T8.1's acceptance
// criterion ("opencode.go exists with an investigation comment,
// regardless of outcome") is satisfied by this file as-is.
//
// Path layout
// -----------
// OpenCode 0.x writes per-user state under XDG_DATA_HOME:
//
//   $XDG_DATA_HOME/opencode/opencode.db        (primary, SQLite)
//   $XDG_DATA_HOME/opencode/opencode.db-shm    (WAL shared-memory)
//   $XDG_DATA_HOME/opencode/opencode.db-wal    (WAL log)
//   $XDG_DATA_HOME/opencode/storage/session_diff/<session_id>.json
//                                              (per-session diff buffer,
//                                              usually empty/transient)
//
// On macOS: $HOME/.local/share/opencode/. Confirmed by inspection of a
// live installation in /Users/leon/.local/share/opencode/. The CLI
// startup logs in `log/` are not relevant for context tracking.
//
// The OpenCode plugin under opencode-plugin/ does not document the
// path; the brainstorm reference to
//   opencode-plugin/node_modules/@opencode-ai/sdk/dist/gen/types.gen.d.ts
// is a TypeScript SDK type definition (AssistantMessage), not a runtime
// path. The opencode-plugin/ in this repo is a published-NPM-package
// thrum integration, not the OpenCode CLI itself; its node_modules
// directory only exists after `npm install` and was empty at the time
// of this investigation. The SDK types ARE useful for confirming the
// per-message token shape (see "Token shape" below).
//
// SQLite schema (relevant tables)
// -------------------------------
//   session(id, ..., time_created, ...)
//   message(id, session_id, time_created, time_updated, data TEXT)
//   part(id, message_id, session_id, time_created, time_updated, data TEXT)
//
// The `message.data` column is a JSON blob. Per-session walk:
//
//   SELECT data FROM message
//   WHERE session_id = ?
//   ORDER BY time_created DESC
//   LIMIT 1;
//
// Latest message wins (cumulative token state is on the LAST assistant
// turn, mirroring the Claude semantic).
//
// Token shape (in message.data JSON)
// ----------------------------------
//   {
//     "role": "assistant",
//     "mode": "build",
//     "agent": "build",
//     "path": { "cwd": "...", "root": "..." },
//     "cost": 0,
//     "tokens": {
//       "total":      21220,
//       "input":      40,
//       "output":     9,
//       "reasoning":  15,
//       "cache": { "write": 0, "read": 21156 }
//     },
//     "modelID":     "glm-4.6",
//     "providerID":  "zai-coding-plan",
//     "time": { "created": ..., "completed": ... },
//     "finish":      "stop" | "tool-calls" | ...
//   }
//
// `tokens.total` is the per-message sum:
//   total == input + output + reasoning + cache.read + cache.write
//
// Verified arithmetically on a real message: input=40, output=9,
// reasoning=15, cache.read=21156, cache.write=0 → 21220 == total. ✓
//
// Cumulative context utilization for UsedPercentage
// -------------------------------------------------
// For a chat-style session, the model on each turn sees the entire
// prior conversation as its prompt. The LAST assistant message's
// `tokens.input + tokens.cache.read + tokens.cache.write` therefore
// approximates the current context-window occupancy. (Cache reads are
// previously-sent context being re-sent; cache writes are new entries
// added to the cache that count against the window.) Plan T8.2 step 2
// specifies the sum of all five components — when computed per-message
// (LAST), that overestimates by output + reasoning of the final turn,
// which is acceptable (over-eager is recoverable; under-eager misses
// thresholds).
//
// Context-window denominator
// --------------------------
// OpenCode does NOT carry `Model.limit.context` directly on the
// AssistantMessage in the SQL row inspected. The SDK type
// (AssistantMessage.modelID + a separate Model.limit.context map) does
// expose it, but the live DB row only carries `modelID` + `providerID`.
// The parser would need a per-model context-window lookup (compile-time
// table) keyed on modelID — small enough to maintain (glm-4.6,
// glm-5.1, claude-*, gpt-*, etc.). Fallback: a conservative 128000
// default if modelID is unrecognized, marked Approximate=true.
//
// Approximate flag: always TRUE for OpenCode. The reconstruction from
// per-message tokens.total has > 1-message lag, and the model-ID →
// context-window lookup may fall back to a default for unknown models.
//
// Architectural concern
// ---------------------
// Decision A (open SQLite from parser) requires:
//   - Add database/sql + sqlite3 driver to contextpoll's dep surface.
//   - Open the DB read-only with shared-cache WAL semantics so the
//     OpenCode writer is not blocked: `file:<path>?mode=ro&_journal=WAL&_busy_timeout=5000`.
//   - Re-evaluate Parser.Matches semantics: SQLite databases identify
//     by the "SQLite format 3\0" magic in the first 16 bytes, so
//     Matches can sniff that without opening sqlite3.
//
// Decision B (skip for v0.11): no code in this package; document the
// path here so any future implementer has the discovery work pre-done.
//
// Both options preserve forward compatibility — `OpenCodeParserV1` is
// a separate file that can be added later without touching the
// Poller substrate.
//
// TODO(CR.8-fallback): T8.2 implementer must choose between resolutions
// A (SQLite-aware parser) and B (skip; OpenCode falls back to manual
// /thrum:restart). T8.1 deliverable is this discovery file only; the
// architectural decision is plan-level (cr_spec / coordinator) not
// implementation-level.

package contextpoll
