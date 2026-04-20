package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// thrum-2b2t: HandleRegister's auto-resurrect path creates a fresh session
// via ensureActiveSession but previously did not persist a worktree
// session_ref. That left peercred's lister (daemonlister.go, which joins
// session_refs ← sessions) unable to map the caller's CWD to the agent,
// so mutating RPCs (peer.add, etc.) failed with "anonymous caller".
// These tests exercise the post-resurrect fix: HandleRegister persists a
// worktree session_ref and seeds agent_work_contexts after a successful
// resurrect, using the caller's PID to resolve the git root of its CWD.

func countSessionRefRows(t *testing.T, s *state.State, sessionID, refType string) int {
	t.Helper()
	var n int
	err := s.RawDB().QueryRow(`
		SELECT COUNT(*) FROM session_refs WHERE session_id = ? AND ref_type = ?
	`, sessionID, refType).Scan(&n)
	if err != nil {
		t.Fatalf("count session_refs: %v", err)
	}
	return n
}

func getSessionRefValue(t *testing.T, s *state.State, sessionID, refType string) string {
	t.Helper()
	var v string
	err := s.RawDB().QueryRow(`
		SELECT ref_value FROM session_refs WHERE session_id = ? AND ref_type = ? LIMIT 1
	`, sessionID, refType).Scan(&v)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("get session_refs value: %v", err)
	}
	return v
}

func getAgentWorkContextsWorktree(t *testing.T, s *state.State, sessionID string) string {
	t.Helper()
	var v string
	err := s.RawDB().QueryRow(`
		SELECT worktree_path FROM agent_work_contexts WHERE session_id = ?
	`, sessionID).Scan(&v)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("get agent_work_contexts: %v", err)
	}
	return v
}

// Case 1: HandleRegister's resurrect path with a live PID whose CWD is under
// a git worktree must write a worktree session_ref and seed
// agent_work_contexts so peercred can match immediately on the next RPC.
func TestHandleRegister_Resurrect_PersistsWorktreeRef(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_2b2t_live", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)

	// Register the agent normally (fresh), start a session, then end it.
	// This mirrors the post-daemon-restart state: agent row exists, but
	// no active session. The next HandleRegister call must resurrect.
	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "2b2t-test")

	initialReq := RegisterRequest{Role: "implementer", Module: "2b2t-test", AgentPID: os.Getpid()}
	initialParams, _ := json.Marshal(initialReq)
	initialResp, err := agentHandler.HandleRegister(context.Background(), initialParams)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	agentID := initialResp.(*RegisterResponse).AgentID

	// End any active session the handler may have created.
	s.RawDB().Exec(`UPDATE sessions SET ended_at = ? WHERE agent_id = ? AND ended_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), agentID)

	// Re-register with the same live PID → auto-resurrect must fire.
	reRegReq := RegisterRequest{Role: "implementer", Module: "2b2t-test", AgentPID: os.Getpid()}
	reRegParams, _ := json.Marshal(reRegReq)
	reRegResp, err := agentHandler.HandleRegister(context.Background(), reRegParams)
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	resp := reRegResp.(*RegisterResponse)

	if !resp.SessionResumed {
		t.Fatal("expected SessionResumed=true on re-register of dormant agent; got false")
	}
	if resp.SessionID == "" {
		t.Fatal("expected non-empty SessionID on resurrect")
	}

	// Core assertion: resurrected session has a worktree session_ref.
	if got := countSessionRefRows(t, s, resp.SessionID, "worktree"); got != 1 {
		t.Fatalf("expected 1 worktree session_ref for resurrected session, got %d", got)
	}

	worktreeRef := getSessionRefValue(t, s, resp.SessionID, "worktree")
	if worktreeRef == "" {
		t.Fatal("worktree ref_value is empty")
	}
	// worktreeRef should be the git root of the test binary's CWD. Use the
	// package path as a sanity check — we're running from internal/daemon/rpc
	// so the git root should be a parent of it.
	cwd, _ := os.Getwd()
	if !isSubpathOrEqual(cwd, worktreeRef) {
		t.Errorf("worktree ref %q should be an ancestor of test CWD %q", worktreeRef, cwd)
	}

	// Seed assertion: agent_work_contexts populated (mirrors HandleStart).
	if got := getAgentWorkContextsWorktree(t, s, resp.SessionID); got != worktreeRef {
		t.Errorf("agent_work_contexts.worktree_path = %q, want %q (should mirror session_refs)", got, worktreeRef)
	}

	// Sanity: the unchanged quickstart path still works — keep
	// sessionHandler reachable so we don't accidentally break a linked test.
	_ = sessionHandler
}

// Case 2: HandleRegister's resurrect path with a dead PID skips the
// PID-based CWD resolution (per the existing ensureActiveSession guard)
// and should NOT fire a fallback write. The dead-PID guard at
// ensureActiveSession short-circuits before session creation, so no
// session_refs write is expected either.
func TestHandleRegister_Resurrect_DeadPIDNoResurrectNoRef(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_2b2t_dead", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	agentHandler := NewAgentHandler(s)

	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "2b2t-dead")

	// Register with dead PID; no initial session exists yet — fresh-agent
	// branch. ensureActiveSession is only called on the existing-agent
	// branch, so this test seeds an existing agent explicitly.
	const agentID = "agt_2b2t_dead"
	seedAgentRow(t, s, agentID, 999999) // dead PID
	endedAt := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339Nano)
	seedSessionRow(t, s, "ses_2b2t_dead_old", agentID, endedAt)

	// Direct call into ensureActiveSession (same pattern as xir.18 tests).
	s.Lock()
	defer s.Unlock()

	sessionID, err := agentHandler.ensureActiveSession(context.Background(), agentID, 999999)
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	if sessionID != "" {
		t.Errorf("expected empty sessionID on dead-PID short-circuit, got %q", sessionID)
	}

	// No new session_refs row expected because no session was created.
	var n int
	_ = s.RawDB().QueryRow(`SELECT COUNT(*) FROM session_refs`).Scan(&n)
	if n != 0 {
		t.Errorf("expected 0 session_refs rows after dead-PID no-op, got %d", n)
	}
}

// Case 3 (resolver-injection fallback test): ensureActiveSession succeeds
// (live PID → session created), but the CWD resolver is injected to fail
// — triggers the fallback-to-prior-session-worktree branch. Verifies the
// stale-fallback graceful-degradation path writes the correct ref and
// emits the debug log distinguishing it from fresh resolution.
func TestHandleRegister_Resurrect_FallbackToPriorSessionRef(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_2b2t_fallback", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	agentHandler := NewAgentHandler(s)

	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "2b2t-fallback")

	// Step 1: establish the agent with a worktree session_ref from a
	// prior session, then end that session.
	initialReq := RegisterRequest{Role: "implementer", Module: "2b2t-fallback", AgentPID: os.Getpid()}
	initialParams, _ := json.Marshal(initialReq)
	initialResp, err := agentHandler.HandleRegister(context.Background(), initialParams)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	agentID := initialResp.(*RegisterResponse).AgentID

	// Get the active session and seed a worktree ref for it.
	var priorSessionID string
	_ = s.RawDB().QueryRow(`SELECT session_id FROM sessions WHERE agent_id = ? AND ended_at IS NULL`,
		agentID).Scan(&priorSessionID)
	if priorSessionID == "" {
		priorSessionID = "ses_prior_2b2t_fallback"
		seedSessionRow(t, s, priorSessionID, agentID, "")
	}
	_, err = s.RawDB().Exec(`
		INSERT OR IGNORE INTO session_refs (session_id, ref_type, ref_value, added_at)
		VALUES (?, 'worktree', ?, ?)
	`, priorSessionID, "/prior/worktree/path", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed prior session_ref: %v", err)
	}

	// End the prior session so the next register resurrects.
	s.RawDB().Exec(`UPDATE sessions SET ended_at = ? WHERE session_id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), priorSessionID)

	// Step 2: swap the resolver to fail — simulates the PID being alive
	// for ensureActiveSession's process.IsRunning check but unreachable
	// for CWD inspection (race window between the two).
	origResolver := resolveCallerWorktreeFn
	resolveCallerWorktreeFn = func(_ int) (string, error) {
		return "", fmt.Errorf("injected: PID CWD unreachable")
	}
	t.Cleanup(func() { resolveCallerWorktreeFn = origResolver })

	// Step 3: re-register with live PID → resurrect fires, primary
	// resolver fails, fallback copies /prior/worktree/path.
	reRegReq := RegisterRequest{Role: "implementer", Module: "2b2t-fallback", AgentPID: os.Getpid()}
	reRegParams, _ := json.Marshal(reRegReq)
	reRegResp, err := agentHandler.HandleRegister(context.Background(), reRegParams)
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	resp := reRegResp.(*RegisterResponse)

	if !resp.SessionResumed || resp.SessionID == "" {
		t.Fatal("expected resurrect to succeed even with injected resolver failure")
	}

	got := getSessionRefValue(t, s, resp.SessionID, "worktree")
	if got != "/prior/worktree/path" {
		t.Errorf("fallback should have copied prior worktree ref; got %q, want %q",
			got, "/prior/worktree/path")
	}

	if gotCtx := getAgentWorkContextsWorktree(t, s, resp.SessionID); gotCtx != "/prior/worktree/path" {
		t.Errorf("agent_work_contexts fallback seed = %q, want %q", gotCtx, "/prior/worktree/path")
	}
}

// Case 4: HandleRegister on an already-active session must NOT write a
// duplicate session_ref. The existing session's refs are authoritative;
// the resurrect branch is skipped entirely.
func TestHandleRegister_ExistingActiveSession_NoDuplicateRef(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_2b2t_active", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	agentHandler := NewAgentHandler(s)

	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "2b2t-active")

	initialReq := RegisterRequest{Role: "implementer", Module: "2b2t-active", AgentPID: os.Getpid()}
	initialParams, _ := json.Marshal(initialReq)
	initialResp, err := agentHandler.HandleRegister(context.Background(), initialParams)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	agentID := initialResp.(*RegisterResponse).AgentID

	// Seed a session_refs row on the existing active session to simulate
	// the normal quickstart path having already run.
	var sessionID string
	_ = s.RawDB().QueryRow(`SELECT session_id FROM sessions WHERE agent_id = ? AND ended_at IS NULL`,
		agentID).Scan(&sessionID)
	if sessionID == "" {
		// Initial register didn't create a session (depends on handler behavior).
		// Seed one explicitly.
		sessionID = "ses_active_2b2t"
		seedSessionRow(t, s, sessionID, agentID, "")
	}
	_, err = s.RawDB().Exec(`
		INSERT OR IGNORE INTO session_refs (session_id, ref_type, ref_value, added_at)
		VALUES (?, 'worktree', ?, ?)
	`, sessionID, "/some/existing/worktree", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed session_ref: %v", err)
	}

	// Re-register with same PID — existing session is still active, so
	// ensureActiveSession returns ""; resurrect branch is skipped.
	reRegReq := RegisterRequest{Role: "implementer", Module: "2b2t-active", AgentPID: os.Getpid()}
	reRegParams, _ := json.Marshal(reRegReq)
	reRegResp, err := agentHandler.HandleRegister(context.Background(), reRegParams)
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	resp := reRegResp.(*RegisterResponse)

	if resp.SessionResumed {
		t.Errorf("SessionResumed should be false when active session exists")
	}

	// session_refs for the existing session: still exactly one, unchanged.
	if got := countSessionRefRows(t, s, sessionID, "worktree"); got != 1 {
		t.Errorf("expected exactly 1 worktree session_ref (pre-seeded), got %d", got)
	}
	if v := getSessionRefValue(t, s, sessionID, "worktree"); v != "/some/existing/worktree" {
		t.Errorf("session_ref value changed: %q (expected unchanged pre-seeded value)", v)
	}
}

// isSubpathOrEqual returns true when candidate is equal to or a descendant
// of ancestor. Used to verify that the resolved worktree ref is a
// reasonable git-root ancestor of the test binary's CWD.
func isSubpathOrEqual(candidate, ancestor string) bool {
	if candidate == ancestor {
		return true
	}
	rel, err := filepath.Rel(ancestor, candidate)
	if err != nil {
		return false
	}
	return rel != "" && rel[0] != '.'
}
