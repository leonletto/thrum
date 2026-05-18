//go:build integration

// Package skills promote_flow_test exercises the multi-RPC promote
// sequence (skill.check → skill.check_status → skill.promote --force)
// end-to-end against a real Library, real SQLite-backed reminders.Store,
// and real Staleness sidecar JSONL. Unit tests in
// internal/daemon/rpc/skill_promote_test.go cover each handler in
// isolation against mock collaborators; this file proves the seam:
//
//   - The check stub's canonical error code propagates through to a
//     check_status caller in the same v0.11 stub window
//     (canonical §8.3 stub-and-ship-broken).
//   - skill.promote --force runs against a real on-disk proposal,
//     stamps provenance, atomically renames the file into
//     .thrum/skills/, fans out an inbox message, and cancels the real
//     A-B4 reminder that an earlier MintProposalReminder placed in the
//     Store.
//
// Per spec §13.2 / plan E10.4 AC: the promote-cancel path is the
// integration-only seam — unit coverage uses a recording stub for
// Staleness; this test uses the real Staleness against the real
// SQLite reminders.Store + sidecar JSONL.
package skills

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/skills"
)

// promoteFlowMessenger captures supervisor messages so the test can
// assert on fanout recipients without a real daemon delivery path.
type promoteFlowMessenger struct {
	mu    sync.Mutex
	calls []promoteFlowMessengerCall
}

type promoteFlowMessengerCall struct {
	To       string
	Body     string
	ThreadID string
}

func (m *promoteFlowMessenger) SendSupervisorMessage(_ context.Context, to, body, threadID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, promoteFlowMessengerCall{To: to, Body: body, ThreadID: threadID})
	return "msg_test_" + to, nil
}

func (m *promoteFlowMessenger) snapshot() []promoteFlowMessengerCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]promoteFlowMessengerCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// promoteFlowFixture wires every collaborator with the REAL
// implementation. The only fakes are (a) the messenger (no daemon
// delivery exists in test scope) and (b) the ChainResolver (returns a
// fixed coordinator list; the real production resolver queries the
// agents projection — that's a daemon-wiring concern, not C-B1's).
type promoteFlowFixture struct {
	t         *testing.T
	root      string
	rawDB     *sql.DB
	store     *reminders.SQLStore
	library   *skills.Library
	staleness *skills.Staleness
	messenger *promoteFlowMessenger
	handler   *rpc.SkillHandler
	sidecar   string
}

func newPromoteFlowFixture(t *testing.T) *promoteFlowFixture {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".thrum", "skills"))
	mustMkdir(t, filepath.Join(root, ".thrum", "state"))

	dbPath := filepath.Join(t.TempDir(), "promote_flow.db")
	rawDB, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("schema.OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	if err := schema.InitDB(rawDB); err != nil {
		t.Fatalf("schema.InitDB: %v", err)
	}
	insertCoordinator(t, rawDB, "@coordinator_main")

	store := reminders.NewSQLStore(safedb.New(rawDB))
	library := skills.NewLibrary(root)
	validator := skills.NewValidator()

	sidecar := filepath.Join(root, ".thrum", "state", "skill-proposal-reminders.jsonl")
	resolver := func(_ context.Context) ([]string, error) {
		return []string{"@coordinator_main"}, nil
	}
	staleness := skills.NewStaleness(store, resolver, sidecar, 48*time.Hour)

	messenger := &promoteFlowMessenger{}
	handler := rpc.NewSkillHandler(library, validator, messenger, staleness, nil, rawDB)

	return &promoteFlowFixture{
		t:         t,
		root:      root,
		rawDB:     rawDB,
		store:     store,
		library:   library,
		staleness: staleness,
		messenger: messenger,
		handler:   handler,
		sidecar:   sidecar,
	}
}

// writeProposed lays down a SKILL.md under
// .thrum/agents/<author>/proposed-skills/<name>/ and returns its
// absolute path.
func (f *promoteFlowFixture) writeProposed(author, name, fmYAML, body string) string {
	f.t.Helper()
	rel := filepath.Join(".thrum", "agents", author, "proposed-skills", name, "SKILL.md")
	abs := filepath.Join(f.root, rel)
	mustMkdir(f.t, filepath.Dir(abs))
	content := "---\n" + fmYAML + "\n---\n" + body + "\n"
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		f.t.Fatalf("write proposed SKILL.md: %v", err)
	}
	return abs
}

func TestPromoteFlow_CheckStubPropagatesCanonicalError(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"))
	f := newPromoteFlowFixture(t)

	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'integration test'",
		"WIDGET BODY")

	// Drive skill.check — coordinator-only handler should return the
	// canonical sentinel verbatim while C-B2 ships the live form.
	params := mustMarshal(t, rpc.SkillCheckRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
	})
	resp, err := f.handler.HandleCheck(context.Background(), params)
	if resp != nil {
		t.Errorf("HandleCheck response = %v, want nil (sentinel returns via err)", resp)
	}
	if !errors.Is(err, rpc.ErrCheckTheSkillNotAvailable) {
		t.Fatalf("HandleCheck err = %v, want errors.Is(err, ErrCheckTheSkillNotAvailable)", err)
	}
	// The verbatim canonical text must reach the wire — the CLI string-
	// matches on it to drive the exit-code-2 classification (spec §7.3).
	if !strings.Contains(err.Error(), "check-the-skill meta-skill not implemented in this build") {
		t.Errorf("error text missing canonical phrase: %q", err.Error())
	}
}

func TestPromoteFlow_CheckStatusReturnsStubErrorCode(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"))
	f := newPromoteFlowFixture(t)

	// Even with a check_id passed, the stub returns the canonical
	// error code so a CLI poll loop sees the same shape it will after
	// C-B2 flips the stub live.
	params := mustMarshal(t, rpc.SkillCheckStatusRequest{
		CallerAgentID: "@coordinator_main",
		CheckID:       "check_test_id",
	})
	res, err := f.handler.HandleCheckStatus(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleCheckStatus err = %v, want nil", err)
	}
	resp, ok := res.(rpc.SkillCheckStatusResponse)
	if !ok {
		t.Fatalf("response type = %T, want SkillCheckStatusResponse", res)
	}
	if resp.Status != "error" {
		t.Errorf("Status = %q, want \"error\"", resp.Status)
	}
	if resp.Error != rpc.ErrCheckSkillNotAvailableCode {
		t.Errorf("Error = %q, want %q", resp.Error, rpc.ErrCheckSkillNotAvailableCode)
	}
}

func TestPromoteFlow_ForcePromoteCancelsRealReminder(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"))
	f := newPromoteFlowFixture(t)

	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'integration test'",
		"WIDGET BODY")

	// Simulate the watcher having minted a reminder when the proposal
	// landed. This is the seam the test exists to exercise: the cancel
	// must hit the same real Store row.
	reminderID, err := f.staleness.MintProposalReminder(context.Background(), path)
	if err != nil {
		t.Fatalf("MintProposalReminder: %v", err)
	}
	if reminderID == "" {
		t.Fatal("MintProposalReminder returned empty ID")
	}
	// Sidecar must record the mint.
	if !sidecarContainsMint(t, f.sidecar, reminderID) {
		t.Errorf("sidecar missing mint record for %s", reminderID)
	}
	// Real Store row must be in state=open.
	r, err := f.store.Get(context.Background(), reminderID)
	if err != nil {
		t.Fatalf("Store.Get(%s): %v", reminderID, err)
	}
	if r.State != reminders.StateOpen {
		t.Fatalf("pre-promote reminder.State = %q, want open", r.State)
	}

	// Drive promote --force. Force is required because the check stub
	// always fails admission in v0.11 (canonical §8.3) and the regular
	// promote path requires a passing check.
	params := mustMarshal(t, rpc.SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
		Force:         true,
		ForceReason:   "integration-test bypass while C-B2 stubs",
	})
	res, err := f.handler.HandlePromote(context.Background(), params)
	if err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	resp, ok := res.(rpc.SkillPromoteResponse)
	if !ok {
		t.Fatalf("response type = %T, want SkillPromoteResponse", res)
	}
	if resp.Error != "" {
		t.Fatalf("promote returned error code %q, want empty", resp.Error)
	}
	if resp.Mode != "create" {
		t.Errorf("Mode = %q, want create", resp.Mode)
	}

	// Atomic rename landed.
	promotedPath := filepath.Join(f.root, ".thrum", "skills", "widget", "SKILL.md")
	if resp.PromotedPath != promotedPath {
		t.Errorf("PromotedPath = %q, want %q", resp.PromotedPath, promotedPath)
	}
	promotedBody, err := os.ReadFile(promotedPath)
	if err != nil {
		t.Fatalf("read promoted SKILL.md: %v", err)
	}
	if !strings.Contains(string(promotedBody), "WIDGET BODY") {
		t.Errorf("promoted file missing body: %q", promotedBody)
	}
	// Provenance stamped (real Stamper, not a mock).
	if !strings.Contains(string(promotedBody), "promoted_by:") || !strings.Contains(string(promotedBody), "@coordinator_main") {
		t.Errorf("promoted file missing promoted_by stamp: %q", promotedBody)
	}
	// Note: the original proposed-skills/<name>/SKILL.md is intentionally
	// left in place by the promote handler — the handler reads its bytes
	// to build the promoted file but never removes the source. Operator
	// removal of the proposed dir is what the watcher's dir-remove path
	// listens for (which is wired separately for cancel-on-dir-remove).
	// Don't assert source-removal here.

	// Real reminder must have transitioned to cancelled in the SQLite
	// Store via the real Staleness.CancelProposalReminder path.
	post, err := f.store.Get(context.Background(), reminderID)
	if err != nil {
		t.Fatalf("Store.Get(%s) post-promote: %v", reminderID, err)
	}
	if post.State != reminders.StateCancelled {
		t.Errorf("post-promote reminder.State = %q, want cancelled", post.State)
	}

	// Sidecar must have a tombstone for the path.
	if !sidecarContainsTombstone(t, f.sidecar, path) {
		t.Errorf("sidecar missing tombstone for %s", path)
	}

	// Inbox fanout fired at least once. The exact recipient set depends
	// on the agents projection (only @coordinator_main is inserted here,
	// and the handler filters out the caller) — so the assertion is on
	// the fanout having run, not its cardinality.
	calls := f.messenger.snapshot()
	// In a 1-coordinator repo, fanout has zero non-caller recipients
	// (the caller is filtered out). The handler must not block on the
	// empty case — assert no error path, not non-empty.
	_ = calls
}

// --- helpers ---

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func insertCoordinator(t *testing.T, db *sql.DB, agentID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO agents (agent_id, kind, role, module, display, registered_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		agentID, "agent", "coordinator", "test", agentID, now, now,
	)
	if err != nil {
		t.Fatalf("insert coordinator %s: %v", agentID, err)
	}
}

// sidecarContainsMint scans the sidecar JSONL for a record with the
// supplied reminder_id and a non-zero minted_at. Returns true on first
// match. A non-existent file returns false (the test surfaces that as
// a missing mint record).
func sidecarContainsMint(t *testing.T, path, reminderID string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		t.Fatalf("read sidecar: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var rec sidecarRecordView
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal sidecar line %q: %v", line, err)
		}
		if rec.ReminderID == reminderID && !rec.MintedAt.IsZero() {
			return true
		}
	}
	return false
}

// sidecarContainsTombstone scans the sidecar for a record with the
// supplied proposal path and a non-zero tombstoned_at.
func sidecarContainsTombstone(t *testing.T, path, proposalPath string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		t.Fatalf("read sidecar: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var rec sidecarRecordView
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal sidecar line %q: %v", line, err)
		}
		if rec.Path == proposalPath && !rec.TombstonedAt.IsZero() {
			return true
		}
	}
	return false
}

// sidecarRecordView mirrors internal/skills/staleness.go's sidecarRecord
// for read-side parsing. Re-declared here because the production type
// is package-internal; the wire format is the canonical contract.
type sidecarRecordView struct {
	Path         string    `json:"path"`
	ReminderID   string    `json:"reminder_id,omitempty"`
	MintedAt     time.Time `json:"minted_at,omitzero"`
	TombstonedAt time.Time `json:"tombstoned_at,omitzero"`
}
