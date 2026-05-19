// OpenCode transcript parser (CR.8 / thrum-6qmf.1.24).
//
// Full transcript-path discovery notes from T8.1 / thrum-6qmf.1.23 (original
// commit e383276f46) are preserved below the implementation. Summary:
//
//   Path:        $HOME/.local/share/opencode/opencode.db (SQLite + WAL)
//   Table:       message(id, session_id, time_created, time_updated, data TEXT)
//   message.data: JSON blob with role, tokens.{total,input,output,reasoning,
//                cache.{read,write}}, modelID, providerID, ...
//   Approximate: TRUE (reconstruction from per-message tokens + model-name
//                lookup that may fall back to default for unknown models).
//
// Path-A (Leon-decision, v1.4 erratum thrum-6qmf.1.31): SQLite reader lives
// inside contextpoll. Concurrent-access concern with the live OpenCode
// writer mitigated by opening read-only with WAL semantics
// (file:<path>?mode=ro). The OpenCode writer keeps opencode.db in WAL
// mode (opencode.db-shm + opencode.db-wal companion files); a read-only
// reader observes the WAL without disturbing it.
//
// SQL: SELECT data FROM message ORDER BY time_created DESC LIMIT 1.
// OpenCode users typically run one active conversation at a time across
// agents — the global-latest message wins. A multi-active-conversation
// user could see attribution skew on a stale agent (warn nudge meant for
// session A delivered to session B's agent), but this is recoverable
// false-warn behavior and the operator-visibility property is preserved.

package contextpoll

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	// modernc.org/sqlite is the project's pure-Go SQLite driver (no CGO).
	// Registers as "sqlite" — not "sqlite3". Plan v1.4 prose-mentions
	// mattn/go-sqlite3 but the project's go.mod has only modernc.org/sqlite
	// and adopting CGO for a single parser would be a bigger ask than the
	// task warrants. URI semantics are SQLite-standard for mode=ro; modernc
	// uses `_pragma=foo(bar)` rather than mattn's `_foo=bar`, hence the
	// PRAGMA-after-open pattern in Parse below for busy_timeout. The
	// Path-A architectural decision is unchanged — only the driver
	// substitution is in play.
	_ "modernc.org/sqlite"
)

// openCodeDefaultContextWindow is the conservative default window used
// when an unrecognized modelID surfaces. 128K is the sweet spot for the
// model families OpenCode supports out of the box (glm-4.6, glm-5.1)
// and is also a safe over-estimate of utilization for the smaller
// 32K/64K models, since over-eager warn nudges are recoverable while
// missed thresholds are not.
const openCodeDefaultContextWindow = 128000

// openCodeContextWindow returns the context-window size for an OpenCode
// model ID. Falls back to openCodeDefaultContextWindow for unrecognized
// model IDs and logs the fallback at Debug so operators can grow the
// lookup table over time without changing the parser's behavior.
//
// The model-ID format in opencode is `<family-version>` (e.g. "glm-4.6",
// "claude-sonnet-4-6", "gpt-5"). The table uses prefix/contains checks
// rather than exact matches to ride out minor-version bumps. Initial
// table content comes from plan v1.4 §CR.8 + a survey of distinct
// modelID values observed in the live OpenCode DB during T8.1 (mostly
// glm-4.6 + glm-5.1).
func openCodeContextWindow(modelID string) int {
	id := strings.ToLower(modelID)
	switch {
	case strings.HasPrefix(id, "glm-"):
		return 128000
	case strings.Contains(id, "claude-opus"), strings.Contains(id, "claude-sonnet"):
		// Claude 4.x context window. Long-context [1m] variants would
		// require a separate tag if OpenCode ever exposes them; today
		// OpenCode reports the bare model id.
		return 200000
	case strings.HasPrefix(id, "gpt-5"):
		return 256000
	case strings.HasPrefix(id, "gpt-4"):
		return 128000
	default:
		slog.Debug("[contextpoll] opencode unknown modelID; falling back to default window",
			"model_id", modelID, "default_window", openCodeDefaultContextWindow)
		return openCodeDefaultContextWindow
	}
}

// OpenCodeParserV1 parses OpenCode session state from the per-user SQLite
// database that the OpenCode CLI maintains at
// $XDG_DATA_HOME/opencode/opencode.db. Unlike ClaudeParserV2x and
// CodexParserV1 which read JSONL files, this parser opens SQLite — the
// Parser.Parse(path) signature is preserved (path == the .db file) so
// the Poller dispatch stays uniform across runtimes.
type OpenCodeParserV1 struct{}

// Version returns the parser version tag used for log attribution.
func (OpenCodeParserV1) Version() string { return "opencode-v1" }

// openCodeMagic is the SQLite database header — the first 16 bytes of
// any SQLite file. Used by Matches as a cheap-and-unambiguous dispatch
// signal: no other runtime we support writes SQLite files into a
// transcript-path position, so this is sufficient to claim a path
// without opening the sqlite3 driver (saves a connection-open on every
// poll for non-OpenCode agents).
var openCodeMagic = []byte("SQLite format 3\x00")

// Matches returns true when path begins with the SQLite header magic.
// Returns false for files that are unreadable, shorter than 16 bytes,
// or whose first 16 bytes do not match. The check does NOT validate
// that the SQLite file is specifically an OpenCode database — a
// future runtime that also writes SQLite would need a stricter probe
// (e.g. a CREATE TABLE statement sniff via the schema). At the time
// of writing OpenCode is the only such runtime.
func (OpenCodeParserV1) Matches(path string) bool {
	f, err := os.Open(path) // #nosec G304 -- path is an absolute SQLite path resolved at agent-enrollment time
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var hdr [16]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return bytes.Equal(hdr[:], openCodeMagic)
}

// openCodeMessageData is the minimal projection of the JSON blob stored
// in message.data needed to compute context utilization. Other fields
// on the wire (parentID, mode, agent, path, cost, providerID, time,
// finish) are deliberately ignored — the parser is schema-tolerant
// by design.
type openCodeMessageData struct {
	Role    string `json:"role"`
	ModelID string `json:"modelID"`
	Tokens  struct {
		Input     int `json:"input"`
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
		Cache     struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

// Parse opens the OpenCode SQLite database at path read-only, fetches
// the most-recently-created message row, decodes its data JSON blob,
// and returns context utilization derived from the per-message token
// counts + a per-model context-window lookup.
//
// Error contract:
//   - File-not-found, open errors, and SQL errors return a non-nil error.
//   - Empty DB (no rows in `message`) returns UsedPercentage 0 with
//     nil error and Approximate=true — a fresh-install OpenCode that
//     hasn't recorded a turn yet.
//   - A row whose data JSON is unparseable returns a non-nil error
//     (corruption or schema drift — surface it; don't silently mask).
//
// UsedPercentage = (input + output + reasoning + cache.read + cache.write)
//
//	* 100 / openCodeContextWindow(modelID)
//
// clamped to [0, 100]. Approximate is always TRUE: reconstruction from
// per-message token totals has > 1-message lag (output + reasoning of
// the last turn are NOT in the context window the model SAW, only what
// it wrote), and the modelID → window lookup may fall back to a
// default.
func (OpenCodeParserV1) Parse(path string) (ContextUsage, error) {
	uri := "file:" + path + "?mode=ro"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return ContextUsage{}, fmt.Errorf("contextpoll: open opencode db: %w", err)
	}
	defer func() { _ = db.Close() }()

	// modernc.org/sqlite honors PRAGMAs issued through the standard
	// database/sql API. busy_timeout gives us 5s of grace if the
	// OpenCode writer is mid-transaction; without it a concurrent
	// write would surface as SQLITE_BUSY immediately.
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return ContextUsage{}, fmt.Errorf("contextpoll: set opencode busy_timeout: %w", err)
	}

	var blob string
	err = db.QueryRow(`SELECT data FROM message ORDER BY time_created DESC LIMIT 1`).Scan(&blob)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Fresh OpenCode install — no turns yet. UsedPercentage 0,
			// no error. The Poller's threshold logic treats 0% as
			// "below warn" and the agent gets no nudge.
			return ContextUsage{
				UsedPercentage: 0,
				ParserVersion:  "opencode-v1",
				SourcePath:     path,
				Timestamp:      time.Now(),
				Approximate:    true,
			}, nil
		}
		return ContextUsage{}, fmt.Errorf("contextpoll: query opencode db: %w", err)
	}

	var msg openCodeMessageData
	if err := json.Unmarshal([]byte(blob), &msg); err != nil {
		return ContextUsage{}, fmt.Errorf("contextpoll: parse opencode message.data: %w", err)
	}

	sumTokens := msg.Tokens.Input + msg.Tokens.Output + msg.Tokens.Reasoning +
		msg.Tokens.Cache.Read + msg.Tokens.Cache.Write
	window := openCodeContextWindow(msg.ModelID)
	if window <= 0 {
		// Defensive: openCodeContextWindow is documented to always return
		// a positive integer (the default falls back to a positive value).
		// Keep the guard so a future code edit can't trip a divide-by-zero.
		window = openCodeDefaultContextWindow
	}
	pct := sumTokens * 100 / window
	pct = max(0, min(100, pct))

	return ContextUsage{
		UsedPercentage: pct,
		ParserVersion:  "opencode-v1",
		SourcePath:     path,
		Timestamp:      time.Now(),
		Approximate:    true,
	}, nil
}

// Full discovery notes (preserved from T8.1 investigation; original
// commit e383276f46). Path-A was selected by Leon (v1.4 erratum
// thrum-6qmf.1.31); the previous TODO(CR.8-fallback) marker is
// removed.
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
// On macOS: $HOME/.local/share/opencode/. Confirmed by inspection of
// a live installation in /Users/leon/.local/share/opencode/. The CLI
// startup logs in `log/` are not relevant for context tracking.
//
// SQLite schema (relevant tables)
// -------------------------------
//   session(id, ..., time_created, ...)
//   message(id, session_id, time_created, time_updated, data TEXT)
//   part(id, message_id, session_id, time_created, time_updated, data TEXT)
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
// Verified arithmetically on a real message: input=40, output=9,
// reasoning=15, cache.read=21156, cache.write=0 → 21220 == total. ✓
// (The Parser doesn't trust `total`; it sums the components itself
// so a future field rename or new component category surfaces as
// an arithmetic discrepancy rather than a silent wrong total.)
