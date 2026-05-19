package contextpoll

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// newOpenCodeFixture builds a temporary SQLite DB at dir/opencode.db with
// the OpenCode schema needed by OpenCodeParserV1.Parse. The schema
// mirrors the real OpenCode schema for the message table (and only the
// columns Parse touches — id, session_id, time_created, data); the
// other OpenCode tables aren't reproduced because the parser doesn't
// query them.
//
// Returns the file path so tests can pass it straight to Parse/Matches.
func newOpenCodeFixture(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
        CREATE TABLE message (
            id           TEXT PRIMARY KEY,
            session_id   TEXT NOT NULL,
            time_created INTEGER NOT NULL,
            time_updated INTEGER NOT NULL,
            data         TEXT NOT NULL
        )
    `)
	if err != nil {
		t.Fatalf("create message table: %v", err)
	}
	return path
}

// insertOpenCodeMessage writes one message row matching the live
// OpenCode wire shape. Latest time_created wins per the SELECT
// ORDER BY time_created DESC LIMIT 1 query.
func insertOpenCodeMessage(t *testing.T, path string, id string, timeCreated int64, modelID string,
	input, output, reasoning, cacheRead, cacheWrite int) {
	t.Helper()
	type cache struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	}
	type tokens struct {
		Input     int   `json:"input"`
		Output    int   `json:"output"`
		Reasoning int   `json:"reasoning"`
		Cache     cache `json:"cache"`
		Total     int   `json:"total"`
	}
	type wire struct {
		Role    string `json:"role"`
		Tokens  tokens `json:"tokens"`
		ModelID string `json:"modelID"`
	}
	blob, err := json.Marshal(wire{
		Role:    "assistant",
		ModelID: modelID,
		Tokens: tokens{
			Input:     input,
			Output:    output,
			Reasoning: reasoning,
			Cache:     cache{Read: cacheRead, Write: cacheWrite},
			Total:     input + output + reasoning + cacheRead + cacheWrite,
		},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		id, "ses_test_session", timeCreated, timeCreated, string(blob),
	)
	if err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}
}

func TestOpenCodeParserV1_Version(t *testing.T) {
	if v := (OpenCodeParserV1{}).Version(); v != "opencode-v1" {
		t.Errorf("Version() = %q, want %q", v, "opencode-v1")
	}
}

func TestOpenCodeParserV1_NormalUsage_GLM(t *testing.T) {
	// glm-4.6 → 128000 window. Inserting 64000 total tokens → 50%.
	path := newOpenCodeFixture(t, t.TempDir())
	insertOpenCodeMessage(t, path, "msg1", 1000, "glm-4.6",
		1000, 100, 50, 62850, 0) // sum = 64000
	usage, err := (OpenCodeParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 50 {
		t.Errorf("UsedPercentage = %d, want 50", usage.UsedPercentage)
	}
	if usage.ParserVersion != "opencode-v1" {
		t.Errorf("ParserVersion = %q, want opencode-v1", usage.ParserVersion)
	}
	if usage.SourcePath != path {
		t.Errorf("SourcePath = %q, want %q", usage.SourcePath, path)
	}
	if !usage.Approximate {
		t.Error("Approximate = false, want true (OpenCode reconstruction is always approximate)")
	}
	if usage.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}
}

// TestOpenCodeParserV1_ClaudeModel pins the model→window dispatch for
// claude-* families: 200K window. 100K tokens = 50%.
func TestOpenCodeParserV1_ClaudeModel(t *testing.T) {
	path := newOpenCodeFixture(t, t.TempDir())
	insertOpenCodeMessage(t, path, "msg1", 1000, "claude-sonnet-4-6",
		0, 0, 0, 100000, 0) // sum = 100000 → 50% of 200K
	usage, err := (OpenCodeParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 50 {
		t.Errorf("UsedPercentage = %d, want 50 for claude-sonnet (200K window)", usage.UsedPercentage)
	}
}

// TestOpenCodeParserV1_GPT5Model pins the model→window dispatch for
// gpt-5: 256K window. 128K tokens = 50%.
func TestOpenCodeParserV1_GPT5Model(t *testing.T) {
	path := newOpenCodeFixture(t, t.TempDir())
	insertOpenCodeMessage(t, path, "msg1", 1000, "gpt-5",
		0, 0, 0, 128000, 0)
	usage, err := (OpenCodeParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 50 {
		t.Errorf("UsedPercentage = %d, want 50 for gpt-5 (256K window)", usage.UsedPercentage)
	}
}

// TestOpenCodeParserV1_UnknownModelFallback confirms the default-window
// path is reached for an unrecognized modelID. The percentage check
// confirms the fallback is exactly openCodeDefaultContextWindow (128K),
// not some unrelated value.
func TestOpenCodeParserV1_UnknownModelFallback(t *testing.T) {
	path := newOpenCodeFixture(t, t.TempDir())
	insertOpenCodeMessage(t, path, "msg1", 1000, "future-model-xyz-9000",
		0, 0, 0, 64000, 0) // sum = 64000 → 50% of 128K default
	usage, err := (OpenCodeParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 50 {
		t.Errorf("UsedPercentage = %d, want 50 (128K fallback for unknown model)", usage.UsedPercentage)
	}
	if !usage.Approximate {
		t.Error("Approximate must be true on fallback-window path")
	}
}

// TestOpenCodeParserV1_LatestMessageWins guards the
// ORDER BY time_created DESC LIMIT 1 contract. Two messages in the
// table; the later one (higher time_created) must drive the result.
func TestOpenCodeParserV1_LatestMessageWins(t *testing.T) {
	path := newOpenCodeFixture(t, t.TempDir())
	insertOpenCodeMessage(t, path, "early", 1000, "glm-4.6", 0, 0, 0, 25600, 0) // 20%
	insertOpenCodeMessage(t, path, "late", 2000, "glm-4.6", 0, 0, 0, 89600, 0)  // 70% — winner
	usage, err := (OpenCodeParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 70 {
		t.Errorf("UsedPercentage = %d, want 70 (latest message wins)", usage.UsedPercentage)
	}
}

// TestOpenCodeParserV1_EmptyDB pins the fresh-install behavior: schema
// present but no rows. Per the v1.4 / v1.5 AC: nil error +
// UsedPercentage 0, Approximate=true.
func TestOpenCodeParserV1_EmptyDB(t *testing.T) {
	path := newOpenCodeFixture(t, t.TempDir())
	usage, err := (OpenCodeParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse on empty DB: %v", err)
	}
	if usage.UsedPercentage != 0 {
		t.Errorf("UsedPercentage = %d, want 0", usage.UsedPercentage)
	}
	if !usage.Approximate {
		t.Error("Approximate must be true even on empty DB")
	}
}

func TestOpenCodeParserV1_NearFull(t *testing.T) {
	// 200K tokens against the 128K glm-4.6 window → should clamp to 100.
	path := newOpenCodeFixture(t, t.TempDir())
	insertOpenCodeMessage(t, path, "msg1", 1000, "glm-4.6", 0, 0, 0, 200000, 0)
	usage, err := (OpenCodeParserV1{}).Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if usage.UsedPercentage != 100 {
		t.Errorf("UsedPercentage = %d, want 100 (clamped)", usage.UsedPercentage)
	}
}

func TestOpenCodeParserV1_FileNotFound(t *testing.T) {
	_, err := (OpenCodeParserV1{}).Parse(filepath.Join(t.TempDir(), "does-not-exist.db"))
	if err == nil {
		t.Fatal("Parse on missing file returned nil error, want non-nil")
	}
}

// TestOpenCodeParserV1_MalformedJSON surfaces the corruption-vs-silent
// boundary: a row whose data column is non-empty but unparseable
// returns an error rather than masquerading as 0%. Matches the
// defensive-error pattern endorsed by cr_spec's M1 finding on the
// Codex parser (now incorporated in plan v1.5).
func TestOpenCodeParserV1_MalformedJSON(t *testing.T) {
	path := newOpenCodeFixture(t, t.TempDir())
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"corrupt", "ses_test", 1000, 1000, "{not valid json{",
	)
	if err != nil {
		t.Fatalf("insert corrupt row: %v", err)
	}
	if _, err := (OpenCodeParserV1{}).Parse(path); err == nil {
		t.Fatal("Parse on corrupt data returned nil error, want non-nil")
	}
}

// TestOpenCodeParserV1_NonSQLiteFile asserts the open-error path: passing
// a path to a non-SQLite file surfaces as a Parse error (the underlying
// sqlite driver fails on file-not-a-database). This is the negative-
// control pair to TestOpenCodeParserV1_NormalUsage — confirms wrong
// runtime dispatch is loud.
func TestOpenCodeParserV1_NonSQLiteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-db.txt")
	if err := os.WriteFile(path, []byte("plain text\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := (OpenCodeParserV1{}).Parse(path); err == nil {
		t.Fatal("Parse on plain text file returned nil error, want non-nil")
	}
}

func TestOpenCodeParserV1_Matches_ValidSQLite(t *testing.T) {
	path := newOpenCodeFixture(t, t.TempDir())
	if !(OpenCodeParserV1{}).Matches(path) {
		t.Error("Matches returned false for a valid SQLite file")
	}
}

// TestOpenCodeParserV1_Matches_TooShort covers files that exist but
// are shorter than the 16-byte SQLite header. io.ReadFull returns
// an error on short read, so Matches returns false.
func TestOpenCodeParserV1_Matches_TooShort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.bin")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if (OpenCodeParserV1{}).Matches(path) {
		t.Error("Matches returned true for a file shorter than the SQLite header")
	}
}

// TestOpenCodeParserV1_Matches_NotSQLite covers files that are exactly
// 16+ bytes but don't begin with the SQLite magic. Most common case:
// a JSONL transcript from another runtime accidentally being passed
// to the OpenCode parser.
func TestOpenCodeParserV1_Matches_NotSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.bin")
	// 16+ bytes of plain text, no SQLite magic prefix.
	if err := os.WriteFile(path, []byte("this is plain text not sqlite at all"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if (OpenCodeParserV1{}).Matches(path) {
		t.Error("Matches returned true for non-SQLite content")
	}
}

func TestOpenCodeParserV1_Matches_MissingFile(t *testing.T) {
	if (OpenCodeParserV1{}).Matches(filepath.Join(t.TempDir(), "nope.db")) {
		t.Error("Matches returned true for a missing file")
	}
}

// TestOpenCodeParserV1_Matches_AgainstClaudeJSONL is the most important
// negative-control: a Claude-format JSONL must NOT match the OpenCode
// parser. Without this guard a future first-Matches-wins reordering
// could silently route Claude transcripts into the SQLite open path
// and surface as cryptic errors.
func TestOpenCodeParserV1_Matches_AgainstClaudeJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "looks-like-claude.jsonl")
	// A typical Claude first line — opens with `{` which is NOT the
	// SQLite magic byte (which is 'S').
	line := `{"type":"user","sessionId":"abc","cwd":"/tmp/something-that-extends-past-16-bytes"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if (OpenCodeParserV1{}).Matches(path) {
		t.Error("OpenCodeParserV1.Matches returned true for a Claude JSONL")
	}
}

// TestOpenCodeContextWindow_DefaultFallback pins the model-lookup
// fallback path independently of Parse — covers the openCodeContextWindow
// helper function for an unrecognized prefix.
func TestOpenCodeContextWindow_DefaultFallback(t *testing.T) {
	got := openCodeContextWindow("totally-unknown-model")
	if got != openCodeDefaultContextWindow {
		t.Errorf("openCodeContextWindow(unknown) = %d, want %d",
			got, openCodeDefaultContextWindow)
	}
}

func TestOpenCodeContextWindow_KnownPrefixes(t *testing.T) {
	cases := []struct {
		modelID string
		want    int
	}{
		{"glm-4.6", 128000},
		{"glm-5.1", 128000},
		{"GLM-4.6", 128000}, // case insensitivity
		{"claude-sonnet-4-6", 200000},
		{"claude-opus-4-7", 200000},
		{"gpt-5", 256000},
		{"gpt-4o", 128000},
	}
	for _, tc := range cases {
		if got := openCodeContextWindow(tc.modelID); got != tc.want {
			t.Errorf("openCodeContextWindow(%q) = %d, want %d", tc.modelID, got, tc.want)
		}
	}
}
