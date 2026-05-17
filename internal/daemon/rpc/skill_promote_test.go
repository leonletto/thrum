package rpc

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/skills"
)

// --- Fakes for HandlePromote collaborators ---

// recordingMessenger captures every SendSupervisorMessage call so tests
// can assert on the fanout recipients + body content.
type recordingMessenger struct {
	mu    sync.Mutex
	calls []messengerCall
	err   error // when non-nil, every Send returns this error
}

type messengerCall struct {
	To       string
	Body     string
	ThreadID string
}

func (m *recordingMessenger) SendSupervisorMessage(_ context.Context, to, body, threadID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	m.calls = append(m.calls, messengerCall{To: to, Body: body, ThreadID: threadID})
	return "msg_test_" + to, nil
}

func (m *recordingMessenger) snapshot() []messengerCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]messengerCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// recordingStaleness captures CancelProposalReminder calls. Mint is
// not exercised at promote-time so the test fake records nothing for
// it; the contract requires it to exist for the interface.
type recordingStaleness struct {
	mu        sync.Mutex
	cancelled []string
	cancelErr error
}

func (s *recordingStaleness) MintProposalReminder(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (s *recordingStaleness) CancelProposalReminder(_ context.Context, proposalPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelErr != nil {
		return s.cancelErr
	}
	s.cancelled = append(s.cancelled, proposalPath)
	return nil
}

func (s *recordingStaleness) cancelledPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.cancelled))
	copy(out, s.cancelled)
	return out
}

// fixedClock returns a closure that always reports the same time.
// Used to make stamped frontmatter / response timestamps deterministic.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// promoteFixture is the standard fixture for promote tests: a SkillHandler
// wired with a real Library, a deterministic stamper, a fake messenger,
// a fake staleness, a captured-logs slog, plus an in-memory DB with a
// coordinator agent row pre-inserted.
type promoteFixture struct {
	t          *testing.T
	root       string
	db         *sql.DB
	messenger  *recordingMessenger
	staleness  *recordingStaleness
	logBuf     *bytes.Buffer
	logger     *slog.Logger
	handler    *SkillHandler
	clockTime  time.Time
}

func newPromoteFixture(t *testing.T) *promoteFixture {
	t.Helper()
	root := t.TempDir()
	// Ensure the canonical .thrum/skills/ dir exists so Library.List doesn't
	// error during the post-promote sanity reads in some tests.
	if err := os.MkdirAll(filepath.Join(root, ".thrum", "skills"), 0o750); err != nil {
		t.Fatalf("mkdir skills root: %v", err)
	}
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "@coordinator_main", "coordinator")

	messenger := &recordingMessenger{}
	staleness := &recordingStaleness{}
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clockTime := time.Date(2026, 5, 17, 18, 30, 0, 0, time.UTC)
	h := NewSkillHandler(
		skills.NewLibrary(root),
		skills.NewValidator(),
		messenger,
		staleness,
		nil, // mirror worker not exercised by promote happy-path tests
		db,
	)
	h.stamper = skills.NewStamper(fixedClock(clockTime))
	h.scanner = skills.NewScanner()
	h.clock = fixedClock(clockTime)
	h.logger = logger

	return &promoteFixture{
		t:         t,
		root:      root,
		db:        db,
		messenger: messenger,
		staleness: staleness,
		logBuf:    logBuf,
		logger:    logger,
		handler:   h,
		clockTime: clockTime,
	}
}

// writeProposed creates a proposed-skill SKILL.md under the fixture's
// .thrum/agents/<author>/proposed-skills/<name>/ path and returns its
// absolute path. fmYAML is the YAML frontmatter (without the --- fences).
func (f *promoteFixture) writeProposed(author, name, fmYAML, body string) string {
	f.t.Helper()
	rel := filepath.Join(".thrum", "agents", author, "proposed-skills", name, "SKILL.md")
	writeSKILL(f.t, f.root, rel, fmYAML, body)
	return filepath.Join(f.root, rel)
}

// writePromoted creates a pre-existing promoted skill at
// .thrum/skills/<name>/SKILL.md, returning its absolute path. Used by
// the edit-mode tests so HandlePromote sees the prior state.
func (f *promoteFixture) writePromoted(name, fmYAML, body string) string {
	f.t.Helper()
	rel := filepath.Join(".thrum", "skills", name, "SKILL.md")
	writeSKILL(f.t, f.root, rel, fmYAML, body)
	return filepath.Join(f.root, rel)
}

// callPromote dispatches HandlePromote with the supplied request and
// returns the typed response (or surfaces a t.Fatal on type-assertion
// failure). The Go-error return is propagated for tests that assert on
// auth-style failures.
func (f *promoteFixture) callPromote(req SkillPromoteRequest) (SkillPromoteResponse, error) {
	f.t.Helper()
	params, err := json.Marshal(req)
	if err != nil {
		f.t.Fatalf("marshal request: %v", err)
	}
	res, err := f.handler.HandlePromote(context.Background(), params)
	if err != nil {
		return SkillPromoteResponse{}, err
	}
	resp, ok := res.(SkillPromoteResponse)
	if !ok {
		f.t.Fatalf("response type = %T, want SkillPromoteResponse", res)
	}
	return resp, nil
}

// readPromoted reads the promoted SKILL.md at .thrum/skills/<name>/SKILL.md
// and returns its raw bytes for content assertions.
func (f *promoteFixture) readPromoted(name string) []byte {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, ".thrum", "skills", name, "SKILL.md"))
	if err != nil {
		f.t.Fatalf("read promoted %s: %v", name, err)
	}
	return data
}

// --- Tests ---

func TestPromote_CreatePath(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'unit test'",
		"WIDGET BODY")

	resp, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
	})
	if err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error code: %s", resp.Error)
	}
	if resp.Mode != "create" {
		t.Errorf("Mode = %q, want create", resp.Mode)
	}
	wantPath := filepath.Join(f.root, ".thrum", "skills", "widget", "SKILL.md")
	if resp.PromotedPath != wantPath {
		t.Errorf("PromotedPath = %q, want %q", resp.PromotedPath, wantPath)
	}
	if !resp.PromotedAt.Equal(f.clockTime) {
		t.Errorf("PromotedAt = %v, want %v", resp.PromotedAt, f.clockTime)
	}
	// Canonical file is at the promoted path.
	body := f.readPromoted("widget")
	if !bytes.Contains(body, []byte("WIDGET BODY")) {
		t.Errorf("promoted file missing body: %q", body)
	}
	// Provenance stamped: promoted_by + created_at populated.
	if !bytes.Contains(body, []byte("promoted_by: '@coordinator_main'")) && !bytes.Contains(body, []byte("promoted_by: \"@coordinator_main\"")) {
		t.Errorf("frontmatter missing promoted_by stamp: %q", body)
	}
}

func TestPromote_EditPath(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	originalCreatedAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	// Pre-existing promoted skill with an explicit created_at to verify
	// the edit-promote preserves it.
	f.writePromoted("widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  promoted_by: '@coordinator_main'\n  created_at: '"+originalCreatedAt.Format(time.RFC3339)+"'\n  trigger_reason: 'original'\n  review:\n    reviewed_by: '@coordinator_main'\n    reviewed_at: '"+originalCreatedAt.Format(time.RFC3339)+"'\n    check_skill_version: '0.0.0-stub'",
		"ORIGINAL BODY")

	// New proposal at the same name (edit).
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget v2\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'unit test edit'",
		"WIDGET BODY V2")

	resp, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID:  "@coordinator_main",
		Path:           path,
		MsgThreadID:    "msg_thread_revisionA",
	})
	if err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	if resp.Mode != "edit" {
		t.Errorf("Mode = %q, want edit", resp.Mode)
	}
	if resp.Review == nil {
		t.Fatal("Review is nil")
	}
	if len(resp.Review.Revisions) != 1 {
		t.Fatalf("Revisions = %d, want 1 new entry", len(resp.Review.Revisions))
	}
	rev := resp.Review.Revisions[0]
	if rev.MsgThreadID != "msg_thread_revisionA" {
		t.Errorf("RevisionEntry.MsgThreadID = %q, want msg_thread_revisionA", rev.MsgThreadID)
	}
	if rev.ProposedBy != "@alice" {
		t.Errorf("RevisionEntry.ProposedBy = %q, want @alice", rev.ProposedBy)
	}
	// reviewed_at must be updated to the edit-promote clock (spec §13.3).
	if !resp.Review.ReviewedAt.Equal(f.clockTime) {
		t.Errorf("Review.ReviewedAt = %v, want %v (edit-promote refresh)", resp.Review.ReviewedAt, f.clockTime)
	}
	// On-disk: created_at preserved.
	body := f.readPromoted("widget")
	if !bytes.Contains(body, []byte(originalCreatedAt.Format(time.RFC3339))) {
		t.Errorf("created_at not preserved on edit: %q", body)
	}
	if !bytes.Contains(body, []byte("WIDGET BODY V2")) {
		t.Errorf("new body not written on edit: %q", body)
	}
}

func TestPromote_CoordinatorOnly(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	insertTestAgent(t, f.db, "@researcher_x", "researcher")
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: w\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'", "BODY")

	_, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@researcher_x",
		Path:          path,
	})
	if err == nil {
		t.Fatal("expected unauthorized error, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("err = %v, want unauthorized", err)
	}
}

func TestPromote_FrontmatterInvalid(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	// Missing thrum.trigger_reason + missing thrum.proposed_by → required.
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget", "BODY")

	resp, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
	})
	if err != nil {
		t.Fatalf("HandlePromote returned Go error (expected response.Error instead): %v", err)
	}
	if resp.Error != ErrFrontmatterInvalidCode {
		t.Errorf("Error = %q, want %q", resp.Error, ErrFrontmatterInvalidCode)
	}
	if len(resp.FrontmatterFindings) == 0 {
		t.Error("FrontmatterFindings should be populated on frontmatter_invalid")
	}
	// Ensure no promoted file was written.
	if _, statErr := os.Stat(filepath.Join(f.root, ".thrum", "skills", "widget", "SKILL.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("promoted file should NOT exist after frontmatter_invalid; stat err = %v", statErr)
	}
}

func TestPromote_SecretScanBlocked(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	// sk_live_ + 24+ chars triggers StripeLiveKey.
	secretBody := "Some doc text\nstripe key: sk_live_abcdefghijklmnopqrstuvwxyz1234\nend" //nolint:gosec // G101: intentional fake secret to exercise the scanner
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'",
		secretBody)

	resp, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
	})
	if err != nil {
		t.Fatalf("HandlePromote returned Go error (expected response.Error instead): %v", err)
	}
	if resp.Error != ErrSecretScanBlockedCode {
		t.Errorf("Error = %q, want %q", resp.Error, ErrSecretScanBlockedCode)
	}
	if len(resp.SecretFindings) == 0 {
		t.Fatal("SecretFindings should be populated on secret_scan_blocked")
	}
	if resp.SecretFindings[0].PatternCategory != "StripeLiveKey" {
		t.Errorf("PatternCategory = %q, want StripeLiveKey", resp.SecretFindings[0].PatternCategory)
	}
	// Privacy invariant: the matched string MUST NOT appear anywhere in
	// the response payload.
	payload, _ := json.Marshal(resp)
	if bytes.Contains(payload, []byte("sk_live_abcdefghijklmnopqrstuvwxyz1234")) {
		t.Errorf("response payload leaked matched secret string: %s", payload)
	}
	// No promoted file written.
	if _, statErr := os.Stat(filepath.Join(f.root, ".thrum", "skills", "widget", "SKILL.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("promoted file should NOT exist after secret_scan_blocked; stat err = %v", statErr)
	}
}

func TestPromote_AllowSecretOverrideRecorded(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	secretBody := "doc\nstripe key: sk_live_abcdefghijklmnopqrstuvwxyz1234\nend" //nolint:gosec // G101: intentional fake secret to exercise override path
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'",
		secretBody)

	resp, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
		AllowSecretPatterns: []AllowedPatternWire{
			{Pattern: `sk_live_[0-9a-zA-Z]+`, Reason: "documented fake key in fixture"},
		},
	})
	if err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("expected promote to succeed with override; got error = %q", resp.Error)
	}
	if resp.Mode != "create" {
		t.Errorf("Mode = %q, want create", resp.Mode)
	}
	if resp.Review == nil || len(resp.Review.SecretScanOverrides) != 1 {
		t.Fatalf("expected 1 secret_scan_override; got %+v", resp.Review)
	}
	ov := resp.Review.SecretScanOverrides[0]
	if ov.Pattern != `sk_live_[0-9a-zA-Z]+` {
		t.Errorf("override.Pattern = %q", ov.Pattern)
	}
	if ov.Reason != "documented fake key in fixture" {
		t.Errorf("override.Reason = %q", ov.Reason)
	}
	if ov.ReviewedBy != "@coordinator_main" {
		t.Errorf("override.ReviewedBy = %q", ov.ReviewedBy)
	}
	// The override also lands on disk via the stamper.
	body := f.readPromoted("widget")
	if !bytes.Contains(body, []byte("secret_scan_overrides:")) {
		t.Errorf("frontmatter missing secret_scan_overrides: %q", body)
	}
}

func TestPromote_AtomicMoveRollbackOnError(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	// Pre-existing promoted skill that the edit-promote will attempt to
	// replace. The rename fails (injected); rollback must restore.
	f.writePromoted("widget",
		"name: widget\ndescription: original\nthrum:\n  proposed_by: '@alice'\n  promoted_by: '@coordinator_main'\n  created_at: '2026-04-01T12:00:00Z'\n  trigger_reason: 'orig'\n  review:\n    reviewed_by: '@coordinator_main'\n    reviewed_at: '2026-04-01T12:00:00Z'\n    check_skill_version: '0.0.0-stub'",
		"ORIGINAL")
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: new\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'edit'",
		"NEW BODY")

	// Inject a failing rename: any rename whose target is the final
	// .thrum/skills/widget/ path fails.
	finalDir := filepath.Join(f.root, ".thrum", "skills", "widget")
	injected := errors.New("injected rename failure")
	f.handler.renameFunc = func(oldpath, newpath string) error {
		if newpath == finalDir {
			return injected
		}
		return os.Rename(oldpath, newpath)
	}

	_, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
	})
	if err == nil {
		t.Fatal("expected promote error from injected rename failure")
	}
	if !strings.Contains(err.Error(), "injected rename failure") {
		t.Errorf("err = %v, want wrapper of injected failure", err)
	}
	// Rollback: original on-disk state restored.
	body, readErr := os.ReadFile(filepath.Join(finalDir, "SKILL.md"))
	if readErr != nil {
		t.Fatalf("original file missing after rollback: %v", readErr)
	}
	if !bytes.Contains(body, []byte("ORIGINAL")) {
		t.Errorf("rollback did NOT restore original content: %q", body)
	}
	// Temp dir is cleaned up.
	if _, statErr := os.Stat(filepath.Join(f.root, ".thrum", "skills", "widget.tmp")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("temp dir should be removed on rollback; stat = %v", statErr)
	}
}

func TestPromote_InboxNotificationFanout(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	// Five additional agents in the repo. supervisor pseudo-agents and
	// user:-prefixed humans should be filtered out of the fanout.
	insertTestAgent(t, f.db, "@agent_a", "researcher")
	insertTestAgent(t, f.db, "@agent_b", "implementer")
	insertTestAgent(t, f.db, "@agent_c", "researcher")
	insertTestAgent(t, f.db, "@agent_d", "tester")
	insertTestAgent(t, f.db, "user:leon", "user")
	insertTestAgent(t, f.db, "supervisor_thrum", "supervisor")

	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: w\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'", "BODY")

	if _, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
	}); err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	calls := f.messenger.snapshot()
	// 5 agents fanout: @coordinator_main + 4 @agent_* (user: and supervisor_ filtered).
	// The handler passes agent_id as stored in the DB (with the @-prefix);
	// permission.SendSupervisorMessage normalises to bare-name internally,
	// but the fake records the raw arg the handler supplied.
	got := map[string]struct{}{}
	for _, c := range calls {
		got[c.To] = struct{}{}
	}
	for _, want := range []string{"@coordinator_main", "@agent_a", "@agent_b", "@agent_c", "@agent_d"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing fanout recipient: %s (got=%v)", want, got)
		}
	}
	if _, ok := got["user:leon"]; ok {
		t.Errorf("user:-prefixed recipient should be filtered, got it in fanout")
	}
	if _, ok := got["supervisor_thrum"]; ok {
		t.Errorf("supervisor pseudo-agent should be filtered, got it in fanout")
	}
}

func TestPromote_CancelsStalenessReminder(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: w\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'", "BODY")

	if _, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
	}); err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	got := f.staleness.cancelledPaths()
	if len(got) != 1 {
		t.Fatalf("CancelProposalReminder calls = %d, want 1", len(got))
	}
	if got[0] != path {
		t.Errorf("cancelled path = %q, want %q", got[0], path)
	}
}

func TestPromote_ForceSkipsCheckButRunsSecretScan(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	secretBody := "doc\ngithub: ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nend" //nolint:gosec // G101: intentional fake secret to confirm --force does NOT bypass secret-scan
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: w\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'",
		secretBody)

	resp, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
		Force:         true,
	})
	if err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	if resp.Error != ErrSecretScanBlockedCode {
		t.Errorf("Error = %q, want %q (force must NOT bypass secret-scan)", resp.Error, ErrSecretScanBlockedCode)
	}
}

func TestPromote_DaemonLogAuditEntry(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: w\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'", "BODY")

	if _, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
	}); err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	logs := f.logBuf.String()
	if !strings.Contains(logs, "skill promoted") {
		t.Errorf("audit log missing 'skill promoted' line: %s", logs)
	}
	for _, want := range []string{`name=widget`, `mode=create`, `caller=@coordinator_main`} {
		if !strings.Contains(logs, want) {
			t.Errorf("audit log missing %q: %s", want, logs)
		}
	}
}

// TestPromote_MessengerErrorDoesNotFailPromote pins the best-effort
// fanout invariant: a Messenger that returns an error per call still
// allows the promote response to return success (the on-disk write is
// already complete and durable). The error surfaces via the audit log.
func TestPromote_MessengerErrorDoesNotFailPromote(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	f.messenger.err = errors.New("supervisor down")
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: w\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'", "BODY")

	resp, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
	})
	if err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	if resp.Error != "" {
		t.Errorf("messenger failure must NOT surface as promote error; got %q", resp.Error)
	}
}

// TestPromote_InvalidPatternStructuredError pins the contract that a
// malformed AllowSecretPatterns regex surfaces via response.Error
// (matching every other coordinator-facing logical failure on this
// verb), not as an opaque Go error wrapped from the scanner. This was
// fix-batch finding #2 from the code-review pass on E10.4.
func TestPromote_InvalidPatternStructuredError(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'", "BODY")

	resp, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
		AllowSecretPatterns: []AllowedPatternWire{
			{Pattern: `[unclosed-class`, Reason: "typo"},
		},
	})
	if err != nil {
		t.Fatalf("HandlePromote returned Go error (expected response.Error instead): %v", err)
	}
	if resp.Error != ErrInvalidPatternCode {
		t.Errorf("Error = %q, want %q", resp.Error, ErrInvalidPatternCode)
	}
	if len(resp.InvalidPatterns) != 1 {
		t.Fatalf("InvalidPatterns = %d, want 1", len(resp.InvalidPatterns))
	}
	if resp.InvalidPatterns[0].Pattern != `[unclosed-class` {
		t.Errorf("InvalidPatterns[0].Pattern = %q, want [unclosed-class", resp.InvalidPatterns[0].Pattern)
	}
	// No promoted file written.
	if _, statErr := os.Stat(filepath.Join(f.root, ".thrum", "skills", "widget", "SKILL.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("promoted file should NOT exist after invalid_pattern; stat err = %v", statErr)
	}
}

// TestPromote_ForceOverrideStampedAndFanout pins plan AC line 1632-1634
// behavior: --force is plumbed but no functional effect in v0.11 (per
// the stub-and-ship-broken decision), AND the operator's reason is
// recorded in review.force_override + every fanout recipient sees a
// FORCE OVERRIDE marker on their inbox notification.
func TestPromote_ForceOverrideStampedAndFanout(t *testing.T) {
	t.Parallel()
	f := newPromoteFixture(t)
	insertTestAgent(t, f.db, "@witness", "researcher")
	path := f.writeProposed("@alice", "widget",
		"name: widget\ndescription: a widget\nthrum:\n  proposed_by: '@alice'\n  trigger_reason: 'test'", "BODY")

	resp, err := f.callPromote(SkillPromoteRequest{
		CallerAgentID: "@coordinator_main",
		Path:          path,
		Force:         true,
		ForceReason:   "C-B2 stub gap; coordinator vouches for content",
	})
	if err != nil {
		t.Fatalf("HandlePromote: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Review == nil || resp.Review.ForceOverride == "" {
		t.Fatalf("Review.ForceOverride empty; want non-empty audit string")
	}
	if !strings.Contains(resp.Review.ForceOverride, "C-B2 stub gap") {
		t.Errorf("ForceOverride = %q; want it to include the operator's reason", resp.Review.ForceOverride)
	}
	if !strings.Contains(resp.Review.ForceOverride, "@coordinator_main") {
		t.Errorf("ForceOverride = %q; want it to include the caller agent id", resp.Review.ForceOverride)
	}
	// Every fanout recipient gets a body prefixed with FORCE OVERRIDE.
	calls := f.messenger.snapshot()
	if len(calls) == 0 {
		t.Fatal("no fanout calls recorded")
	}
	for _, c := range calls {
		if !strings.Contains(c.Body, "FORCE OVERRIDE") {
			t.Errorf("recipient %s body missing FORCE OVERRIDE marker: %q", c.To, c.Body)
		}
	}
}

// Compile-time guard: io.Discard import keeps unused-import linters
// quiet if a future refactor removes the only consumer.
var _ = io.Discard
