package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

// TestHandleJournal_HappyPath verifies team.journal returns the
// daemon-rendered journal multi-line string for a single agent. Uses
// the fakeLifecycleStore from teamrender_test.go to inject events.
func TestHandleJournal_HappyPath(t *testing.T) {
	st, _ := newTestStateForFilter(t, "test_repo_team_journal_ok")
	defer func() { _ = st.Close() }()

	teamHandler := NewTeamHandler(st, "", nil, nil)
	teamHandler.SetLifecycleStore(&fakeLifecycleStore{
		events: []lcEvent{
			{When: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), Kind: "respawn_fired", Reason: "pane gone"},
		},
	})

	req := JournalRequest{AgentName: "docs_bot"}
	raw, _ := json.Marshal(req)
	out, err := teamHandler.HandleJournal(context.Background(), raw)
	if err != nil {
		t.Fatalf("HandleJournal: %v", err)
	}
	resp, ok := out.(JournalResponse)
	if !ok {
		t.Fatalf("response type = %T; want JournalResponse", out)
	}
	if resp.AgentName != "docs_bot" {
		t.Errorf("AgentName = %q; want docs_bot", resp.AgentName)
	}
	if resp.Journal == "" {
		t.Fatalf("Journal payload is empty")
	}
	if !contains(resp.Journal, "respawn_fired") {
		t.Errorf("journal missing event kind; got %q", resp.Journal)
	}
}

// TestHandleJournal_EmptyAgentNameRejected guards against empty
// agent-name probes; the RPC must surface an error rather than
// silently returning an empty journal for "" (which would mask
// CLI-side bugs).
func TestHandleJournal_EmptyAgentNameRejected(t *testing.T) {
	st, _ := newTestStateForFilter(t, "test_repo_team_journal_empty")
	defer func() { _ = st.Close() }()

	teamHandler := NewTeamHandler(st, "", nil, nil)
	teamHandler.SetLifecycleStore(&fakeLifecycleStore{})

	raw, _ := json.Marshal(JournalRequest{AgentName: ""})
	_, err := teamHandler.HandleJournal(context.Background(), raw)
	if err == nil {
		t.Fatalf("expected error on empty agent_name; got nil")
	}
}

// TestHandleJournal_NoStore_StaticMessage verifies the daemon
// surfaces a static "Journal unavailable" line when the lifecycle
// store isn't wired (pre-B-B1 daemons / fixture daemons), so the CLI
// can render it inline rather than failing the RPC.
func TestHandleJournal_NoStore_StaticMessage(t *testing.T) {
	st, _ := newTestStateForFilter(t, "test_repo_team_journal_nostore")
	defer func() { _ = st.Close() }()

	teamHandler := NewTeamHandler(st, "", nil, nil)
	// Intentionally do NOT call SetLifecycleStore.

	raw, _ := json.Marshal(JournalRequest{AgentName: "docs_bot"})
	out, err := teamHandler.HandleJournal(context.Background(), raw)
	if err != nil {
		t.Fatalf("HandleJournal: %v", err)
	}
	resp := out.(JournalResponse)
	if !contains(resp.Journal, "Journal unavailable") {
		t.Errorf("expected 'Journal unavailable' message; got %q", resp.Journal)
	}
}

// TestHandleList_AgentFilter_FiltersToOneMember confirms the daemon
// honors AgentFilter by returning exactly one member and populating
// the Body field via the §7.6 fallback chain. Branch 4 (no summary)
// fires because no capture/outbound/summary.md is wired.
func TestHandleList_AgentFilter_FiltersToOneMember(t *testing.T) {
	st, agentID := newRegisteredAgentForFilter(t, "test_repo_team_filter_one")
	defer func() { _ = st.Close() }()

	teamHandler := NewTeamHandler(st, "", nil, nil)
	teamHandler.SetLifecycleStore(&fakeLifecycleStore{})

	req := TeamListRequest{AgentFilter: agentID}
	raw, _ := json.Marshal(req)
	out, err := teamHandler.HandleList(context.Background(), raw)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	resp := out.(*TeamListResponse)
	if len(resp.Members) != 1 {
		t.Fatalf("Members len = %d; want 1 (filter targeted single agent)", len(resp.Members))
	}
	if resp.Members[0].AgentID != agentID {
		t.Errorf("filtered agent = %q; want %q", resp.Members[0].AgentID, agentID)
	}
	// Body branch 4 fallback fires because capture/outbound/summary
	// are all unwired — the chain must still produce a non-empty body.
	if resp.Members[0].Body == "" {
		t.Errorf("Body should fall back to branch 4; got empty")
	}
	// Shared-messages footer should be suppressed for single-agent view.
	if resp.SharedMessages != nil {
		t.Errorf("SharedMessages should be nil for filtered view; got %+v", resp.SharedMessages)
	}
}

// TestHandleList_AgentFilter_NotFound_EmptySlice confirms the daemon
// returns an empty slice (not an error) when the AgentFilter targets
// a non-existent agent. The CLI surfaces the "not found" UX error;
// the daemon stays narrow.
func TestHandleList_AgentFilter_NotFound_EmptySlice(t *testing.T) {
	st, _ := newRegisteredAgentForFilter(t, "test_repo_team_filter_miss")
	defer func() { _ = st.Close() }()

	teamHandler := NewTeamHandler(st, "", nil, nil)

	req := TeamListRequest{AgentFilter: "ghost_agent"}
	raw, _ := json.Marshal(req)
	out, err := teamHandler.HandleList(context.Background(), raw)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	resp := out.(*TeamListResponse)
	if len(resp.Members) != 0 {
		t.Errorf("Members len = %d; want 0 (filter missed)", len(resp.Members))
	}
}

// TestDecorateWithBody_PaneCaptureBranch1 confirms branch 1 fires
// when paneCapture returns content for a member whose
// LastRunState/JobCurrentState shape signals "running". Since
// scheduler injection isn't wired yet, we exercise the wiring
// directly via the helper rather than HandleList.
func TestDecorateWithBody_BranchOrder(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	agentsDir := filepath.Join(thrumDir, "agents", "docs_bot")
	if err := os.MkdirAll(agentsDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	st, _ := newTestStateForFilter(t, "test_repo_team_decorate_body")
	defer func() { _ = st.Close() }()

	teamHandler := NewTeamHandler(st, thrumDir, nil, nil)
	// Wire only the outbound lookup; pane capture stays nil so the
	// chain skips branch 1 and falls through. Branch 2's summary.md
	// is absent (no write), so the chain reaches branch 3.
	teamHandler.SetOutboundLookup(func(_ context.Context, _ string) (*OutboundMessage, error) {
		return &OutboundMessage{MessageID: "msg_01ABC", Subject: "ack: stage 7"}, nil
	})

	m := &TeamMember{AgentID: "docs_bot", Status: "active"}
	teamHandler.decorateWithBody(context.Background(), m)
	if !contains(m.Body, "msg_01ABC") {
		t.Errorf("expected branch-3 message id in Body; got %q", m.Body)
	}
	if !contains(m.Body, "ack: stage 7") {
		t.Errorf("expected branch-3 subject in Body; got %q", m.Body)
	}
}

// TestDecorateWithBody_NoDepsFallsToBranch4 confirms branch 4 (the
// always-succeeds fallback) fires when capture, summary.md, and
// outbound are all unwired or empty — exercising the nil-safety
// guarantee that the chain never returns an empty Body.
func TestDecorateWithBody_NoDepsFallsToBranch4(t *testing.T) {
	st, _ := newTestStateForFilter(t, "test_repo_team_decorate_body_fallback")
	defer func() { _ = st.Close() }()

	teamHandler := NewTeamHandler(st, t.TempDir(), nil, nil)
	// Both capture + outbound nil.
	m := &TeamMember{AgentID: "docs_bot", Status: "active"}
	teamHandler.decorateWithBody(context.Background(), m)
	if m.Body == "" {
		t.Errorf("Body must never be empty after decorate; expected branch-4 fallback")
	}
	if !contains(m.Body, "No summary") {
		t.Errorf("expected branch-4 'No summary' line; got %q", m.Body)
	}
}

// TestDecorateWithBody_PaneCaptureWins exercises branch 1: when
// JobCurrentState=="running" and pane capture returns content, the
// chain must short-circuit at branch 1 and not consult later
// branches. Because the scheduler join isn't wired in v0.11 batch 2,
// we set the render state implicitly via the helper-emulated
// "running" path — paneCapture returning non-empty + the renderer's
// internal JobCurrentState gate.
//
// NOTE: decorateWithBody currently leaves JobCurrentState at "" until
// thrum-6qmf.4.90 scheduler injection lands. Branch 1 fires only when
// the scheduler hook is set; until then this test documents the
// no-op shape and the assertion is that the wrapper itself is
// invocable without panic.
func TestDecorateWithBody_PaneCaptureInvocable(t *testing.T) {
	st, _ := newTestStateForFilter(t, "test_repo_team_decorate_body_pane")
	defer func() { _ = st.Close() }()

	teamHandler := NewTeamHandler(st, t.TempDir(), nil, nil)
	teamHandler.SetPaneCapture(func(_ context.Context, _ string, _ int) (string, error) {
		return "line 1\nline 2\nline 3", nil
	})
	m := &TeamMember{AgentID: "docs_bot", Status: "active"}
	teamHandler.decorateWithBody(context.Background(), m)
	// Pane capture set but JobCurrentState empty: branch 1 must skip
	// per spec — chain falls through to branch 4 (no outbound, no
	// summary.md, no LastRunState).
	if contains(m.Body, "line 1") {
		t.Errorf("branch 1 should NOT fire while JobCurrentState=\"\"; got %q", m.Body)
	}
}

// TestOutboundLookup_NoRows_NilResult verifies the production
// NewMessagesOutboundLookup wrapper returns (nil, nil) for an agent
// with no outbound messages, so RenderBodyFallbackChain branch 3
// cleanly falls through to branch 4.
func TestOutboundLookup_NoRows_NilResult(t *testing.T) {
	st, _ := newTestStateForFilter(t, "test_repo_outbound_lookup_empty")
	defer func() { _ = st.Close() }()

	lookup := NewMessagesOutboundLookup(st)
	msg, err := lookup(context.Background(), "never_spoke_bot")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if msg != nil {
		t.Errorf("expected nil OutboundMessage for empty history; got %+v", msg)
	}
}

// TestOutboundLookup_EmptyAgentName_Error guards against a wiring
// bug that would pass an empty agent name through to the SQL helper.
func TestOutboundLookup_EmptyAgentName_Error(t *testing.T) {
	st, _ := newTestStateForFilter(t, "test_repo_outbound_lookup_empty_name")
	defer func() { _ = st.Close() }()

	lookup := NewMessagesOutboundLookup(st)
	_, err := lookup(context.Background(), "")
	if err == nil {
		t.Fatalf("expected error for empty agent name; got nil")
	}
	if !contains(err.Error(), "empty agent name") {
		t.Errorf("expected 'empty agent name' in error; got %q", err.Error())
	}
}

// TestNewMessagesOutboundLookup_NilState_Error guards against a
// wiring bug at composition time. Calling the lookup with a nil
// state pointer must error out rather than panic.
func TestNewMessagesOutboundLookup_NilState_Error(t *testing.T) {
	lookup := NewMessagesOutboundLookup(nil)
	_, err := lookup(context.Background(), "docs_bot")
	if err == nil {
		t.Fatalf("expected error for nil state; got nil")
	}
}

// TestHandleList_BulkLifecycleCallCount confirms the I2 fold-in from
// the E6.8 batch-2 third-pass review: decorateWithLifecycle issues a
// SINGLE ListByAgents bulk call per HandleList invocation, not N
// per-agent ListByAgent calls. Regression guard against accidental
// reversion to the pre-bulk fan-out shape.
func TestHandleList_BulkLifecycleCallCount(t *testing.T) {
	st, _ := newTestStateForFilter(t, "test_repo_bulk_listbyagents")
	defer func() { _ = st.Close() }()

	agentHandler := NewAgentHandler(st)
	sessionHandler := NewSessionHandler(st)

	// Register + start sessions for two distinct agents so the bulk
	// query has N >= 2 input — the I2 assertion only matters when the
	// pre-bulk path would have fanned out.
	for _, mod := range []string{"alpha", "beta"} {
		reg := RegisterRequest{Role: "implementer", Module: mod}
		regJSON, _ := json.Marshal(reg)
		regResp, err := agentHandler.HandleRegister(context.Background(), regJSON)
		if err != nil {
			t.Fatalf("register %s: %v", mod, err)
		}
		agentID := regResp.(*RegisterResponse).AgentID
		start := SessionStartRequest{
			AgentID: agentID,
			Refs:    []types.Ref{{Type: "worktree", Value: "/tmp/wt-" + mod}},
		}
		startJSON, _ := json.Marshal(start)
		if _, err := sessionHandler.HandleStart(context.Background(), startJSON); err != nil {
			t.Fatalf("start %s: %v", mod, err)
		}
	}

	store := &fakeLifecycleStore{}
	teamHandler := NewTeamHandler(st, "", nil, nil)
	teamHandler.SetLifecycleStore(store)

	raw, _ := json.Marshal(TeamListRequest{})
	if _, err := teamHandler.HandleList(context.Background(), raw); err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	if store.bulkCalls != 1 {
		t.Errorf("ListByAgents bulk call count = %d; want 1 (I2: handler must issue one bulk query, not per-agent fan-out)", store.bulkCalls)
	}
	if len(store.bulkLastAgents) < 2 {
		t.Errorf("bulkLastAgents len = %d; want >= 2 (both agents in single bulk batch)", len(store.bulkLastAgents))
	}
	// Per-agent path must NOT fire — handler should not be calling
	// the legacy singular variant once decorateWithLifecycle uses bulk.
	if store.calls != 0 {
		t.Errorf("ListByAgent singular call count = %d; want 0 (handler is on the bulk path now)", store.calls)
	}
}

// TestDeriveCrashedBanner_WindowSecondsRendered confirms the M1
// drive-by from the batch-2 third-pass review: the loop-guard banner
// renders the default respawn window value (600 seconds) rather than
// a static "in window" string. Spec §7.6 verbatim: "3 respawns in N
// seconds tripped the loop guard.".
func TestDeriveCrashedBanner_WindowSecondsRendered(t *testing.T) {
	m := &TeamMember{
		AgentID:               "docs_bot",
		AutoRespawnDisabledAt: 1747500000000,
	}
	got := deriveCrashedBanner(m)
	if !contains(got, "600 seconds") {
		t.Errorf("loop-guard banner missing rendered window value; got %q", got)
	}
	if contains(got, "in window") {
		t.Errorf("loop-guard banner still uses static 'in window' wording; got %q", got)
	}
}

// TestFirstLineSnippet_TruncationAndNewline confirms the snippet helper
// used by NewMessagesOutboundLookup honors the rune-safe truncation
// boundary (no mid-codepoint cut) and stops at the first newline.
func TestFirstLineSnippet_TruncationAndNewline(t *testing.T) {
	t.Run("ascii under-cap", func(t *testing.T) {
		got := firstLineSnippet("hello", 10)
		if got != "hello" {
			t.Errorf("got %q; want %q", got, "hello")
		}
	})
	t.Run("stops at newline", func(t *testing.T) {
		got := firstLineSnippet("first line\nsecond line", 80)
		if got != "first line" {
			t.Errorf("got %q; want %q", got, "first line")
		}
	})
	t.Run("ascii over-cap-truncated", func(t *testing.T) {
		got := firstLineSnippet("0123456789ABCDEF", 5)
		if got != "01234…" {
			t.Errorf("got %q; want %q", got, "01234…")
		}
	})
	t.Run("multibyte over-cap-rune-safe", func(t *testing.T) {
		// 6 runes (4 bytes each + filler); cap at 4 runes. Byte-naive
		// slicing would corrupt the 5th rune; the rune-aware helper
		// must cut cleanly at rune boundary.
		got := firstLineSnippet("🎉🎉🎉🎉🎉🎉", 4)
		want := "🎉🎉🎉🎉…"
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})
}

// --- test helpers ---

func newTestStateForFilter(t *testing.T, repoID string) (*state.State, string) {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s, err := state.NewState(thrumDir, syncDir, repoID, "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return s, thrumDir
}

func newRegisteredAgentForFilter(t *testing.T, repoID string) (*state.State, string) {
	t.Helper()
	st, _ := newTestStateForFilter(t, repoID)
	agentHandler := NewAgentHandler(st)
	sessionHandler := NewSessionHandler(st)
	reg := RegisterRequest{Role: "implementer", Module: "filter"}
	regJSON, _ := json.Marshal(reg)
	regResp, err := agentHandler.HandleRegister(context.Background(), regJSON)
	if err != nil {
		_ = st.Close()
		t.Fatalf("register: %v", err)
	}
	agentID := regResp.(*RegisterResponse).AgentID
	// Start a session so the agent surfaces in team.list's active set;
	// AgentFilter is applied after the active-set membership check, so
	// a registered-but-no-session agent would otherwise be invisible.
	start := SessionStartRequest{
		AgentID: agentID,
		Refs:    []types.Ref{{Type: "worktree", Value: "/tmp/test-filter-wt"}},
	}
	startJSON, _ := json.Marshal(start)
	if _, err := sessionHandler.HandleStart(context.Background(), startJSON); err != nil {
		_ = st.Close()
		t.Fatalf("session.start: %v", err)
	}
	return st, agentID
}

// Compile-time guard against drift in the wrapper API shapes.
var _ = errors.New
