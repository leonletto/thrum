package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/permission"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// newPermissionTestHandler constructs a TmuxHandler wired to a real
// state.State + permission.Permission in a temp directory. It also
// seeds an identity file so findIdentityForSession returns a proper
// (name, tmuxTarget) pair. Returns the handler and the Permission so
// tests can drive assertions against the store and the fake keystroke
// sender.
func newPermissionTestHandler(t *testing.T, sessionName string) (*TmuxHandler, *permission.Permission) {
	t.Helper()
	t.Setenv("THRUM_HOME", "")

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_TESTCHECKPANE", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Seed an identity whose tmux_session matches the test's session.
	idFile := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "researcher_" + sessionName,
			Role:   "researcher",
			Module: "cursor-test",
		},
		TmuxSession: sessionName + ":0.0",
		Runtime:     "cursor",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	p := permission.New(st, st.RawDB(), "supervisor_test", "test", thrumDir)

	handler := NewTmuxHandler(thrumDir, st)
	handler.SetPermission(p)
	return handler, p
}

func TestTmuxHandler_HandleStatus_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750)

	handler := NewTmuxHandler(thrumDir, nil)

	result, err := handler.HandleStatus(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("HandleStatus: %v", err)
	}

	resp, ok := result.(*TmuxStatusResponse)
	if !ok {
		t.Fatalf("expected *TmuxStatusResponse, got %T", result)
	}

	if len(resp.Sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(resp.Sessions))
	}
}

func TestTmuxHandler_HandleStatus_WithIdentities(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	os.MkdirAll(identitiesDir, 0750)

	// Create an identity with tmux_session set (session won't exist — should be "dead")
	idFile := config.IdentityFile{
		Version:     4,
		TmuxSession: "thrum-unit-test-nonexistent-session:0.0",
		Runtime:     "claude",
		Agent: config.AgentConfig{
			Name:   "test_agent",
			Role:   "implementer",
			Module: "api",
		},
		Branch: "feature/test",
	}
	data, _ := json.MarshalIndent(idFile, "", "  ")
	os.WriteFile(filepath.Join(identitiesDir, "test_agent.json"), data, 0600)

	handler := NewTmuxHandler(thrumDir, nil)
	result, err := handler.HandleStatus(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("HandleStatus: %v", err)
	}

	resp := result.(*TmuxStatusResponse)
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(resp.Sessions))
	}

	info := resp.Sessions[0]
	if info.Name != "thrum-unit-test-nonexistent-session" {
		t.Errorf("Name = %q, want %q", info.Name, "thrum-unit-test-nonexistent-session")
	}
	if info.Agent != "test_agent" {
		t.Errorf("Agent = %q, want %q", info.Agent, "test_agent")
	}
	if info.State != "dead" {
		t.Errorf("State = %q, want %q (session doesn't exist)", info.State, "dead")
	}
	if info.Runtime != "claude" {
		t.Errorf("Runtime = %q, want %q", info.Runtime, "claude")
	}
}

func TestTmuxHandler_HandleStatus_NoIdentitiesDir(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	// Don't create identities dir

	handler := NewTmuxHandler(thrumDir, nil)
	result, err := handler.HandleStatus(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("HandleStatus: %v", err)
	}

	resp := result.(*TmuxStatusResponse)
	if len(resp.Sessions) != 0 {
		t.Errorf("expected 0 sessions with no identities dir, got %d", len(resp.Sessions))
	}
}

func TestTmuxHandler_HandleCreate_MissingFields(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)

	// Missing name
	_, err := handler.HandleCreate(context.Background(), json.RawMessage(`{"cwd":"/tmp"}`))
	if err == nil {
		t.Error("expected error for missing name")
	}

	// Missing cwd
	_, err = handler.HandleCreate(context.Background(), json.RawMessage(`{"name":"test"}`))
	if err == nil {
		t.Error("expected error for missing cwd")
	}
}

func TestTmuxHandler_HandleCreate_MissingQuickstartFlags(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)
	// No agent_name, role, module, no no_agent flag
	params := json.RawMessage(`{"name":"test-session","cwd":"` + t.TempDir() + `"}`)
	_, err := handler.HandleCreate(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing quickstart flags")
	}
	if !strings.Contains(err.Error(), "quickstart flags required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTmuxHandler_HandleCreate_NoAgentSkipsValidation(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)
	cwd := t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"name":     "test-session",
		"cwd":      cwd,
		"no_agent": true,
	})
	// This will fail at CreateSession (no real tmux) but should pass
	// quickstart validation — error must NOT be about missing flags
	_, err := handler.HandleCreate(context.Background(), json.RawMessage(params))
	if err != nil && strings.Contains(err.Error(), "quickstart flags required") {
		t.Error("should not require quickstart flags when no_agent=true")
	}
	// Verify no redirect was created (--no-agent skips redirect setup)
	if _, statErr := os.Stat(filepath.Join(cwd, ".thrum", "redirect")); !os.IsNotExist(statErr) {
		t.Error(".thrum/redirect should not be created with --no-agent")
	}
}

// TestBuildInlineQuickstartCmd_AlwaysEmitsNoAgentPID closes the
// coverage gap between 'BuildQuickstartCmd flag wiring' (worktree pkg)
// and 'HandleCreate actually invokes BuildQuickstartCmd with the flag
// set'. A regression that silently dropped --no-agent-pid from
// HandleCreate's call site would silently re-introduce thrum-x6e8.6.
// This test pins the invariant at the HandleCreate-facing seam.
func TestBuildInlineQuickstartCmd_AlwaysEmitsNoAgentPID(t *testing.T) {
	req := TmuxCreateRequest{
		AgentName: "impl_test",
		Role:      "implementer",
		Module:    "testing",
		Intent:    "test intent",
		Runtime:   "claude",
	}
	cmd := buildInlineQuickstartCmd(req)
	if !strings.Contains(cmd, "--no-agent-pid") {
		t.Errorf("HandleCreate's inline quickstart must emit --no-agent-pid, got: %s", cmd)
	}
	// Defense-in-depth: a no-op shape doesn't satisfy the assertion.
	// The command must still carry the agent identity fields so the
	// inline invocation actually registers something.
	for _, need := range []string{"--name 'impl_test'", "--role 'implementer'", "--module 'testing'"} {
		if !strings.Contains(cmd, need) {
			t.Errorf("expected %q in quickstart command, got: %s", need, cmd)
		}
	}
}

// TestBuildInlineQuickstartCmd_AlwaysEmitsRepoFlag closes the regression
// from thrum-tc4w: daemon-spawned panes inherit THRUM_HOME from the
// daemon, and an inline `thrum quickstart` without --repo lets the
// cobra root's EffectiveRepoPath substitute flagRepo to THRUM_HOME —
// silently writing the new agent's identity into the wrong worktree.
// HandleCreate must always forward req.Cwd as --repo.
func TestBuildInlineQuickstartCmd_AlwaysEmitsRepoFlag(t *testing.T) {
	req := TmuxCreateRequest{
		Cwd:       "/path/to/worktree",
		AgentName: "impl_test",
		Role:      "implementer",
		Module:    "testing",
	}
	cmd := buildInlineQuickstartCmd(req)
	if !strings.HasPrefix(cmd, "thrum --repo '/path/to/worktree' quickstart ") {
		t.Errorf("expected --repo <cwd> before quickstart, got: %s", cmd)
	}
}

// TestBuildInlineQuickstartCmd_EmptyOptionalFields verifies the command
// degrades gracefully when intent/runtime are not supplied.
// HandleCreate happily accepts empty optional fields, so the emission
// must still carry --no-agent-pid and the required identity flags.
func TestBuildInlineQuickstartCmd_EmptyOptionalFields(t *testing.T) {
	req := TmuxCreateRequest{
		AgentName: "impl_test",
		Role:      "implementer",
		Module:    "testing",
	}
	cmd := buildInlineQuickstartCmd(req)
	if !strings.Contains(cmd, "--no-agent-pid") {
		t.Errorf("expected --no-agent-pid even with empty intent/runtime, got: %s", cmd)
	}
	if strings.Contains(cmd, "--intent") {
		t.Errorf("expected no --intent when empty, got: %s", cmd)
	}
	if strings.Contains(cmd, "--runtime") {
		t.Errorf("expected no --runtime when empty, got: %s", cmd)
	}
}

func TestTmuxHandler_HandleCreate_NotAWorktree(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)
	// cwd is a regular dir (no .git file), not a worktree
	cwd := t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"name":       "test-session",
		"cwd":        cwd,
		"agent_name": "test_agent",
		"role":       "implementer",
		"module":     "test",
	})
	_, err := handler.HandleCreate(context.Background(), json.RawMessage(params))
	if err == nil {
		t.Fatal("expected error for non-worktree cwd")
	}
	if !strings.Contains(err.Error(), "is not a git worktree") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTmuxHandler_HandleLaunch_MissingName(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)

	_, err := handler.HandleLaunch(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestTmuxHandler_HandleLaunch_NoSession(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)

	_, err := handler.HandleLaunch(context.Background(), json.RawMessage(`{"name":"nonexistent"}`))
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestTmuxHandler_ClearTmuxFromIdentities(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	os.MkdirAll(identitiesDir, 0750)

	// Create identity with tmux_session set
	idFile := config.IdentityFile{
		Version:     4,
		TmuxSession: "target-session:0.0",
		Runtime:     "claude",
		Agent:       config.AgentConfig{Name: "agent1", Role: "impl", Module: "test"},
	}
	data, _ := json.MarshalIndent(idFile, "", "  ")
	os.WriteFile(filepath.Join(identitiesDir, "agent1.json"), data, 0600)

	handler := NewTmuxHandler(thrumDir, nil)
	handler.clearTmuxFromIdentities("target-session")

	// Verify tmux_session was cleared
	updated, _ := os.ReadFile(filepath.Join(identitiesDir, "agent1.json"))
	var reloaded config.IdentityFile
	json.Unmarshal(updated, &reloaded)

	if reloaded.TmuxSession != "" {
		t.Errorf("TmuxSession should be empty after clear, got %q", reloaded.TmuxSession)
	}
	if reloaded.Runtime != "" {
		t.Errorf("Runtime should be empty after clear, got %q", reloaded.Runtime)
	}
}

// TestRuntimeToLaunchCmd verifies that launch commands come from runtime
// presets, not hardcoded strings. Regression guard for thrum-xgww: the cursor
// runtime previously fell through to return the runtime name "cursor",
// producing "command not found" in the tmux pane.
func TestRuntimeToLaunchCmd(t *testing.T) {
	tests := []struct {
		runtime string
		want    string
	}{
		{"claude", "claude"},
		{"codex", "codex"},
		{"opencode", "opencode"},
		{"cursor", "agent"},
		{"gemini", "gemini"},
		{"shell", ""},
		{"unknown-runtime", "unknown-runtime"}, // fallback
	}
	for _, tt := range tests {
		t.Run(tt.runtime, func(t *testing.T) {
			got := runtimeToLaunchCmd(tt.runtime)
			if got != tt.want {
				t.Errorf("runtimeToLaunchCmd(%q) = %q, want %q", tt.runtime, got, tt.want)
			}
		})
	}
}

// --- HandleCheckPane permission dispatch tests (Task 7.1) ---

func TestParsePermissionReason(t *testing.T) {
	cases := []struct {
		in          string
		runtime     string
		name        string
		ok          bool
		description string
	}{
		{"permission:cursor.not_in_allowlist", "cursor", "not_in_allowlist", true, "happy path"},
		{"permission:codex.proceed_prompt", "codex", "proceed_prompt", true, "another runtime"},
		{"permission:opencode.permission_required", "opencode", "permission_required", true, "multi-word name"},
		{"permission:cursor.multi.dot.name", "cursor", "multi.dot.name", true, "pattern with dots keeps first split"},
		{"", "", "", false, "empty"},
		{"permission:", "", "", false, "missing runtime and name"},
		{"permission:cursor", "", "", false, "missing dot"},
		{"permission:.name", "", "", false, "empty runtime"},
		{"permission:cursor.", "", "", false, "empty name"},
		{"something_else", "", "", false, "missing prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			runtime, name, ok := parsePermissionReason(tc.in)
			if ok != tc.ok {
				t.Errorf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && (runtime != tc.runtime || name != tc.name) {
				t.Errorf("got (%q, %q), want (%q, %q)", runtime, name, tc.runtime, tc.name)
			}
		})
	}
}

func TestHandleCheckPane_PermissionDispatchesToScheduler(t *testing.T) {
	handler, p := newPermissionTestHandler(t, "cursor-test")

	req := CheckPaneRequest{
		Session: "cursor-test",
		Reason:  "permission:cursor.not_in_allowlist",
		Content: "Run this command?\nNot in allowlist: curl https://example.com\n → Run (once) (y)",
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleCheckPane(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleCheckPane: %v", err)
	}
	checkResp, ok := resp.(*CheckPaneResponse)
	if !ok {
		t.Fatalf("expected *CheckPaneResponse, got %T", resp)
	}
	if checkResp.State != "permission" {
		t.Errorf("State = %q, want permission", checkResp.State)
	}

	// A nudge row should now exist for this session.
	row, err := p.Store().LookupPendingNudgeBySession(context.Background(), "cursor-test")
	if err != nil {
		t.Fatalf("LookupPendingNudgeBySession: %v", err)
	}
	if row == nil {
		t.Fatal("expected a nudge row after first detection")
	}
	if row.AgentName != "researcher_cursor-test" {
		t.Errorf("AgentName = %q, want researcher_cursor-test", row.AgentName)
	}
	if row.PatternKey != "cursor.not_in_allowlist" {
		t.Errorf("PatternKey = %q", row.PatternKey)
	}
	if row.TmuxTarget != "cursor-test:0.0" {
		t.Errorf("TmuxTarget = %q, want cursor-test:0.0", row.TmuxTarget)
	}
	if row.ApproveKey != "y" {
		t.Errorf("ApproveKey = %q", row.ApproveKey)
	}
}

func TestHandleCheckPane_MalformedReasonDoesNotCrash(t *testing.T) {
	handler, p := newPermissionTestHandler(t, "cursor-test")

	// A reason string that trips the parser — no nudge row should
	// be inserted, but the RPC still returns cleanly with state=permission.
	req := CheckPaneRequest{
		Session: "cursor-test",
		Reason:  "permission:badformat",
		Content: "whatever",
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleCheckPane(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleCheckPane: %v", err)
	}
	checkResp := resp.(*CheckPaneResponse)
	if checkResp.State != "permission" {
		t.Errorf("State = %q, want permission", checkResp.State)
	}
	row, _ := p.Store().LookupPendingNudgeBySession(context.Background(), "cursor-test")
	if row != nil {
		t.Error("malformed reason should not insert a nudge row")
	}
}

func TestHandleCheckPane_UnknownPatternDoesNotCrash(t *testing.T) {
	handler, p := newPermissionTestHandler(t, "cursor-test")

	// Parses correctly but the runtime.name combo has no matching
	// Pattern — no row should be inserted.
	req := CheckPaneRequest{
		Session: "cursor-test",
		Reason:  "permission:cursor.nonexistent_pattern_name",
		Content: "whatever",
	}
	params, _ := json.Marshal(req)
	if _, err := handler.HandleCheckPane(context.Background(), params); err != nil {
		t.Fatalf("HandleCheckPane: %v", err)
	}
	row, _ := p.Store().LookupPendingNudgeBySession(context.Background(), "cursor-test")
	if row != nil {
		t.Error("unknown pattern should not insert a nudge row")
	}
}

func TestHandleCheckPane_IdleTriggersOnRecovery(t *testing.T) {
	handler, p := newPermissionTestHandler(t, "cursor-test")
	ctx := context.Background()

	// Seed a pending nudge row for the session.
	now := time.Now().UTC()
	row := &permission.NudgeRow{
		MessageID:     "msg_prior_nudge",
		Session:       "cursor-test",
		TmuxTarget:    "cursor-test:0.0",
		AgentName:     "researcher_cursor-test",
		PatternKey:    "cursor.not_in_allowlist",
		ApproveKey:    "y",
		DenyKey:       "Escape",
		FirstDetected: now,
		LastNudgeAt:   now,
		NudgeCount:    1,
		LastPaneHash:  sha256.Sum256([]byte("stale")),
		ExpiresAt:     now.Add(8 * time.Hour),
	}
	if err := p.Store().InsertPendingNudge(ctx, row); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	// Issue an idle check-pane. The scheduler should delete the row.
	req := CheckPaneRequest{
		Session: "cursor-test",
		Reason:  "",
		Content: "",
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleCheckPane(ctx, params)
	if err != nil {
		t.Fatalf("HandleCheckPane: %v", err)
	}
	checkResp := resp.(*CheckPaneResponse)
	if checkResp.State != "idle" {
		t.Errorf("State = %q, want idle", checkResp.State)
	}

	gone, _ := p.Store().LookupPendingNudgeBySession(ctx, "cursor-test")
	if gone != nil {
		t.Errorf("expected row to be removed by OnRecovery, got %+v", gone)
	}
}

func TestHandleCheckPane_NoPermissionWired_PermissionPathIsNoOp(t *testing.T) {
	// Existing tests construct TmuxHandler without calling
	// SetPermission. Those call sites must continue to work: the
	// permission branch should guard on nil and fall through
	// returning state=permission with no side effects.
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)

	handler := NewTmuxHandler(thrumDir, nil)
	req := CheckPaneRequest{
		Session: "cursor-test",
		Reason:  "permission:cursor.not_in_allowlist",
		Content: "whatever",
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleCheckPane(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleCheckPane with nil permission: %v", err)
	}
	if resp.(*CheckPaneResponse).State != "permission" {
		t.Error("state should still be permission even without wiring")
	}
}

// TestHandleCheckPane_CommandCompletedAlsoRunsOnRecovery verifies that
// when an active queue command causes paneState to flip to
// "command_completed", OnRecovery still fires and cleans up any
// pending nudge row for the session (thrum-4ten regression guard).
func TestHandleCheckPane_CommandCompletedAlsoRunsOnRecovery(t *testing.T) {
	handler, p := newPermissionTestHandler(t, "cursor-test")
	ctx := context.Background()

	// Seed a pending nudge row (simulates a prior permission prompt that
	// the agent has already dismissed on its own).
	now := time.Now().UTC()
	row := &permission.NudgeRow{
		MessageID:     "msg_recovery_test",
		Session:       "cursor-test",
		TmuxTarget:    "cursor-test:0.0",
		AgentName:     "researcher_cursor-test",
		PatternKey:    "cursor.not_in_allowlist",
		ApproveKey:    "y",
		DenyKey:       "Escape",
		FirstDetected: now,
		LastNudgeAt:   now,
		NudgeCount:    1,
		LastPaneHash:  sha256.Sum256([]byte("stale")),
		ExpiresAt:     now.Add(8 * time.Hour),
	}
	if err := p.Store().InsertPendingNudge(ctx, row); err != nil {
		t.Fatalf("seed nudge row: %v", err)
	}

	// Seed an active queue command for this session (simulates the
	// Escape keystroke sent via "thrum tmux send cursor-test Escape").
	// SetActive places the command in the active slot so Active()
	// returns it and HandleCheckPane's queue branch fires.
	queue := handler.getOrCreateQueue("cursor-test")
	activeCmd := &QueuedCommand{
		ID:          "cmd_test_escape",
		Text:        "Escape",
		State:       StateSent,
		SubmittedAt: now,
		SentAt:      now,
	}
	queue.SetActive(activeCmd)

	// Fire a check-pane with an idle reason — the queue branch will
	// flip paneState to "command_completed". OnRecovery must STILL
	// fire and delete the nudge row.
	req := CheckPaneRequest{
		Session: "cursor-test",
		Reason:  "",
		Content: "",
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleCheckPane(ctx, params)
	if err != nil {
		t.Fatalf("HandleCheckPane: %v", err)
	}
	checkResp := resp.(*CheckPaneResponse)

	// Queue branch must have fired (paneState should be command_completed).
	if checkResp.State != "command_completed" {
		t.Errorf("State = %q, want command_completed", checkResp.State)
	}

	// Nudge row must be gone (OnRecovery fired despite non-idle paneState).
	gone, err := p.Store().LookupPendingNudgeBySession(ctx, "cursor-test")
	if err != nil {
		t.Fatalf("LookupPendingNudgeBySession: %v", err)
	}
	if gone != nil {
		t.Errorf("expected nudge row to be deleted by OnRecovery after command_completed, but row still exists: %+v", gone)
	}
}

// TestHandleCheckPane_DetectionFromContent verifies that when the CLI
// does not pre-compute a reason (the production path since the
// CLI→server detection handoff), HandleCheckPane resolves the agent's
// runtime from the identity file and runs DetectPaneState itself.
// This is the single-source-of-truth path: the CLI ignores --repo
// and only ever knows the session name, so the daemon is the only
// layer that can authoritatively resolve (session → identity → runtime)
// before pattern matching.
func TestHandleCheckPane_DetectionFromContent(t *testing.T) {
	handler, p := newPermissionTestHandler(t, "cursor-test")

	// CLI sends content but no reason. The handler must compute
	// reason="permission:cursor.not_in_allowlist" from the identity's
	// runtime + the pane content and dispatch to OnDetection.
	req := CheckPaneRequest{
		Session: "cursor-test",
		Reason:  "",
		Content: "Run this command?\n  Not in allowlist: curl https://example.com\n → Run (once) (y)",
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleCheckPane(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleCheckPane: %v", err)
	}
	checkResp := resp.(*CheckPaneResponse)
	if checkResp.State != "permission" {
		t.Errorf("State = %q, want permission (server-side detection should have fired)", checkResp.State)
	}
	if checkResp.Reason != "permission:cursor.not_in_allowlist" {
		t.Errorf("Reason = %q, want permission:cursor.not_in_allowlist", checkResp.Reason)
	}
	row, err := p.Store().LookupPendingNudgeBySession(context.Background(), "cursor-test")
	if err != nil {
		t.Fatalf("LookupPendingNudgeBySession: %v", err)
	}
	if row == nil {
		t.Fatal("expected a nudge row after server-side detection")
	}
	if row.PatternKey != "cursor.not_in_allowlist" {
		t.Errorf("PatternKey = %q", row.PatternKey)
	}
}

// TestHandleCheckPane_DetectionFromContent_NoMatch verifies that
// server-side detection with pane content that does not match any
// runtime pattern falls through to the idle path (OnRecovery).
func TestHandleCheckPane_DetectionFromContent_NoMatch(t *testing.T) {
	handler, p := newPermissionTestHandler(t, "cursor-test")

	req := CheckPaneRequest{
		Session: "cursor-test",
		Reason:  "",
		Content: "just some normal shell output, nothing permission-like\n$ ls\nfile.txt",
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleCheckPane(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleCheckPane: %v", err)
	}
	checkResp := resp.(*CheckPaneResponse)
	if checkResp.State != "idle" {
		t.Errorf("State = %q, want idle", checkResp.State)
	}
	row, _ := p.Store().LookupPendingNudgeBySession(context.Background(), "cursor-test")
	if row != nil {
		t.Error("no-match content should not insert a nudge row")
	}
}

// TestHandleCheckPane_IdentityMissingTmuxSession_SilentlyIdles is a
// NEGATIVE-SPACE CONTRACT test for thrum-enlw.8. The assertion below
// asserts the pre-fix BROKEN behavior on purpose — findIdentityForSession
// in tmux.go matches on idFile.TmuxSession, so an identity file with an
// empty tmux_session field MUST NOT match a session lookup, even when
// runtime is populated and the pane content would match a pattern. That
// non-match is the invariant that makes the quickstart-side fix
// (cmd/thrum/main.go populates tmux_session before first SaveIdentityFile)
// the sole source of truth for session-identity mapping.
//
// If someone adds a fallback match path in findIdentityForSession — for
// example matching by Worktree or by a cross-reference to the tmux
// server's session list when TmuxSession is empty — this test will fail,
// forcing a revisit of the contract: either accept that fallback as the
// new design (and rewrite this test to match), or recognize that the
// fallback re-introduces the silent-idle failure mode on ANY identity
// that happens to have TmuxSession unset for unrelated reasons.
func TestHandleCheckPane_IdentityMissingTmuxSession_SilentlyIdles(t *testing.T) {
	t.Setenv("THRUM_HOME", "")

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_MISSINGTMUX", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Seed an identity WITHOUT TmuxSession — the exact state a
	// newly-quickstarted agent would have before the thrum-enlw.8 fix.
	// Runtime IS set, which is necessary to isolate the TmuxSession-
	// lookup gap as the culprit (not a missing-runtime short-circuit).
	idFile := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "impl_buggy",
			Role:   "implementer",
			Module: "buggy",
		},
		Runtime: "cursor",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	p := permission.New(st, st.RawDB(), "supervisor_test", "test", thrumDir)
	handler := NewTmuxHandler(thrumDir, st)
	handler.SetPermission(p)

	req := CheckPaneRequest{
		Session: "cursor-test",
		Reason:  "",
		Content: "Run this command?\n  Not in allowlist: curl https://example.com\n → Run (once) (y)",
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleCheckPane(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleCheckPane: %v", err)
	}
	checkResp := resp.(*CheckPaneResponse)

	// Contract: with TmuxSession empty on the only identity file,
	// findIdentityForSession cannot match "cursor-test" and the guard
	// at tmux.go:549 skips DetectPaneState. The handler returns idle
	// with no reason despite the pane content matching a pattern.
	if checkResp.State != "idle" {
		t.Errorf("State = %q, want idle (identity has empty tmux_session; session-match should fail)", checkResp.State)
	}
	if checkResp.Reason != "" {
		t.Errorf("Reason = %q, want empty (detection should be skipped, not fallthrough)", checkResp.Reason)
	}
	row, _ := p.Store().LookupPendingNudgeBySession(context.Background(), "cursor-test")
	if row != nil {
		t.Error("no nudge row should be created when identity lookup fails")
	}
}
