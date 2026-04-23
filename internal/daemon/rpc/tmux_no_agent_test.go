package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// newNoAgentTestHandler builds a TmuxHandler wired to a temp .thrum
// dir with an empty identities/ directory. Use this when the test
// wants to exercise HandleSend or HandleStatus WITHOUT any identity
// files present (the --no-agent session case).
func newNoAgentTestHandler(t *testing.T) (*TmuxHandler, string) {
	t.Helper()
	t.Setenv("THRUM_HOME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return NewTmuxHandler(thrumDir, nil), thrumDir
}

// stubTmuxSeams overrides the package-level seams used by HandleSend
// and HandleStatus second-pass and restores them on cleanup.
func stubTmuxSeams(t *testing.T,
	has func(string) bool,
	send func(string, string) error,
	list func() ([]string, error),
	getOpt func(string, string) (string, error),
) {
	t.Helper()
	prevHas, prevSend, prevList, prevGet := hasSessionFn, sendKeysFn, listSessionsFn, getUserOptionFn
	t.Cleanup(func() {
		hasSessionFn = prevHas
		sendKeysFn = prevSend
		listSessionsFn = prevList
		getUserOptionFn = prevGet
	})
	if has != nil {
		hasSessionFn = has
	}
	if send != nil {
		sendKeysFn = send
	}
	if list != nil {
		listSessionsFn = list
	}
	if getOpt != nil {
		getUserOptionFn = getOpt
	}
}

// TestHandleSend_NoAgentBypassesQueue verifies that a tmux.send RPC
// against a session with no registered agent does NOT hit the queue
// path (which would reject with "queue requires an agent-managed
// session"). Instead it must do a raw SendKeys and return a non-error
// response. Regression guard for thrum-ufv5.12 (blocks Step 10D.11).
func TestHandleSend_NoAgentBypassesQueue(t *testing.T) {
	handler, _ := newNoAgentTestHandler(t)

	var gotTarget, gotText string
	var sendCalls int
	stubTmuxSeams(t,
		func(name string) bool { return true }, // session exists
		func(target, text string) error {
			sendCalls++
			gotTarget = target
			gotText = text
			return nil
		},
		nil, nil,
	)

	params, _ := json.Marshal(TmuxSendRequest{Name: "foo-nopid", Text: "echo hi"})
	resp, err := handler.HandleSend(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleSend: %v", err)
	}
	if _, ok := resp.(*QueueResponse); !ok {
		t.Fatalf("expected *QueueResponse response shape, got %T", resp)
	}
	if sendCalls != 1 {
		t.Fatalf("sendKeysFn calls = %d, want 1", sendCalls)
	}
	if gotTarget != "foo-nopid:0.0" {
		t.Errorf("target = %q, want %q", gotTarget, "foo-nopid:0.0")
	}
	if gotText != "echo hi" {
		t.Errorf("text = %q, want %q", gotText, "echo hi")
	}
}

// TestHandleSend_NoAgentPropagatesSendFailure ensures that a SendKeys
// error on the no-agent bypass path surfaces to the caller instead of
// being silently swallowed.
func TestHandleSend_NoAgentPropagatesSendFailure(t *testing.T) {
	handler, _ := newNoAgentTestHandler(t)

	stubTmuxSeams(t,
		func(string) bool { return true },
		func(string, string) error { return errors.New("boom") },
		nil, nil,
	)

	params, _ := json.Marshal(TmuxSendRequest{Name: "bare", Text: "echo"})
	_, err := handler.HandleSend(context.Background(), params)
	if err == nil || (!strings.Contains(err.Error(), "send-keys") && !strings.Contains(err.Error(), "boom")) {
		t.Fatalf("expected send-keys failure, got %v", err)
	}
}

// TestHandleSend_AgentManagedStillRoutesThroughQueue proves the
// agent-managed flow is untouched — if findIdentityForSession returns
// a non-empty agent name, HandleSend must NOT call sendKeysFn directly
// (that would skip queue/@system semantics). We assert this by stubbing
// sendKeysFn to fail the test if called, then driving a request that
// WILL fail inside HandleQueue (because no state is wired) — any
// non-queue error or a sendKeysFn call would be a regression.
func TestHandleSend_AgentManagedStillRoutesThroughQueue(t *testing.T) {
	handler, thrumDir := newNoAgentTestHandler(t)

	// Queue path requires a live state for position-counting; wire one
	// so the call reaches tmux dispatch instead of panicking on nil.
	st, err := state.NewState(thrumDir, thrumDir, "r_TESTNOAGENT", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	handler.state = st

	// Seed an identity matching the session so findIdentityForSession
	// returns non-empty.
	idFile := &config.IdentityFile{
		Agent:       config.AgentConfig{Name: "agent_a", Role: "implementer", Module: "x"},
		TmuxSession: "managed-1:0.0",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	stubTmuxSeams(t,
		func(string) bool { return true },
		func(target, text string) error {
			t.Fatalf("bypass SendKeys must not run for agent-managed session (target=%q)", target)
			return nil
		},
		nil, nil,
	)

	params, _ := json.Marshal(TmuxSendRequest{Name: "managed-1", Text: "echo"})
	// With state wired, the queue path will accept the command (the
	// in-memory queue does not shell out to tmux on submission when
	// the session is not silent). Success here — with no bypass
	// SendKeys calls — proves routing is through the queue.
	resp, callErr := handler.HandleSend(context.Background(), params)
	if callErr != nil {
		t.Fatalf("HandleSend (agent-managed): %v", callErr)
	}
	qr, ok := resp.(*QueueResponse)
	if !ok || qr.CommandID == "" {
		t.Fatalf("expected populated *QueueResponse, got %#v", resp)
	}
}

// TestHandleStatus_IncludesNoAgentManagedSessions verifies the second
// pass: sessions discovered via tmux's `@thrum-managed=1` tag appear
// in the status response with empty Agent and State=alive. Regression
// guard for thrum-ufv5.11 (Step 10C.1 / 10C.7).
func TestHandleStatus_IncludesNoAgentManagedSessions(t *testing.T) {
	handler, _ := newNoAgentTestHandler(t)

	stubTmuxSeams(t,
		func(string) bool { return true },
		nil,
		func() ([]string, error) { return []string{"bare-1", "unrelated", "bare-2"}, nil },
		func(sess, key string) (string, error) {
			if key != "@thrum-managed" {
				return "", fmt.Errorf("unexpected key %q", key)
			}
			switch sess {
			case "bare-1", "bare-2":
				return "1", nil
			default:
				return "", errors.New("option not set")
			}
		},
	)

	resp, err := handler.HandleStatus(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("HandleStatus: %v", err)
	}
	list := resp.(*TmuxStatusResponse).Sessions
	if len(list) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(list), list)
	}
	byName := map[string]TmuxSessionInfo{}
	for _, s := range list {
		byName[s.Name] = s
	}
	for _, want := range []string{"bare-1", "bare-2"} {
		info, ok := byName[want]
		if !ok {
			t.Fatalf("missing session %q", want)
		}
		if info.Agent != "" {
			t.Errorf("%s: Agent = %q, want empty", want, info.Agent)
		}
		if info.State != "alive" {
			t.Errorf("%s: State = %q, want alive", want, info.State)
		}
	}
	if _, bad := byName["unrelated"]; bad {
		t.Error("unrelated session must not leak into thrum tmux status")
	}
}

// TestHandleStatus_DeduplicatesBetweenIdentityAndTagPasses ensures the
// second pass does not double-list a session that also has an
// identity file — the `seen` map spans both passes.
func TestHandleStatus_DeduplicatesBetweenIdentityAndTagPasses(t *testing.T) {
	handler, thrumDir := newNoAgentTestHandler(t)

	idFile := &config.IdentityFile{
		Version:     4,
		Agent:       config.AgentConfig{Name: "agent_a", Role: "implementer", Module: "x"},
		TmuxSession: "shared:0.0",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	stubTmuxSeams(t,
		func(string) bool { return true },
		nil,
		func() ([]string, error) { return []string{"shared"}, nil },
		func(_, _ string) (string, error) { return "1", nil },
	)

	resp, err := handler.HandleStatus(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("HandleStatus: %v", err)
	}
	list := resp.(*TmuxStatusResponse).Sessions
	if len(list) != 1 {
		t.Fatalf("expected 1 deduplicated session, got %d: %+v", len(list), list)
	}
	if list[0].Agent != "agent_a" {
		t.Errorf("Agent = %q, want agent_a (identity pass must win)", list[0].Agent)
	}
}

// TestHandleCreate_TagFailureRollsBackSession verifies the rollback
// invariant from thrum-ufv5.11 review #4: if SetUserOption fails to
// tag a newly-created session, HandleCreate must kill the session and
// return an error. Leaving an untagged session alive would break the
// "--no-agent sessions appear in status" contract for its lifetime
// (no identity-file fallback exists for bare sessions).
func TestHandleCreate_TagFailureRollsBackSession(t *testing.T) {
	if _, err := os.Stat("/usr/bin/tmux"); err != nil {
		if _, err := os.Stat("/opt/homebrew/bin/tmux"); err != nil {
			t.Skip("tmux binary not available on this host")
		}
	}

	handler, _ := newNoAgentTestHandler(t)
	cwd := t.TempDir()

	prevSet, prevKill := setUserOptionFn, killSessionFn
	t.Cleanup(func() {
		setUserOptionFn = prevSet
		killSessionFn = prevKill
	})

	setUserOptionFn = func(string, string, string) error {
		return errors.New("simulated set-option failure")
	}
	var killCalls []string
	killSessionFn = func(name string) error {
		killCalls = append(killCalls, name)
		// Still destroy the real tmux session so the test doesn't leak.
		return prevKill(name)
	}

	params, _ := json.Marshal(map[string]any{
		"name":     "rollback-probe",
		"cwd":      cwd,
		"no_agent": true,
	})
	_, err := handler.HandleCreate(context.Background(), json.RawMessage(params))
	if err == nil {
		// Clean up the dangling session before failing.
		_ = prevKill("rollback-probe")
		t.Fatal("expected error when set-option fails; got nil")
	}
	if !strings.Contains(err.Error(), "tag session") {
		t.Errorf("error = %v, want contains 'tag session'", err)
	}
	if len(killCalls) != 1 || killCalls[0] != "rollback-probe" {
		t.Errorf("killSessionFn calls = %v, want [rollback-probe]", killCalls)
	}
}

// TestHandleSend_RejectsEmptyFields ensures HandleSend validates
// required fields before attempting any routing decision.
func TestHandleSend_RejectsEmptyFields(t *testing.T) {
	handler, _ := newNoAgentTestHandler(t)
	cases := []struct {
		req  TmuxSendRequest
		want string
	}{
		{TmuxSendRequest{Name: "", Text: "x"}, "name is required"},
		{TmuxSendRequest{Name: "s", Text: ""}, "text is required"},
	}
	for _, tc := range cases {
		params, _ := json.Marshal(tc.req)
		_, err := handler.HandleSend(context.Background(), params)
		if err == nil || err.Error() != tc.want {
			t.Errorf("req=%+v err=%v want=%q", tc.req, err, tc.want)
		}
	}
}
