package cli

import "testing"

func tmuxCtx(post bool, cwd string, force bool, state StateAccessor) HintCtx {
	return HintCtx{
		Command: "tmux.create",
		Args:    []string{"foo"},
		Flags:   map[string]any{"cwd": cwd, "force": force},
		Post:    post,
		State:   state,
	}
}

func Test_tmuxCreateHints_sessionExists(t *testing.T) {
	state := &MockState{
		TmuxSessions:     map[string]bool{"foo": true},
		GitWorktrees:     map[string]bool{"/w": true},
		IdentityStatuses: map[string]MockIdentity{"/w": {Status: IdentityNone}},
	}
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if !containsCode(hs, HintTmuxCreateSessionExists) {
		t.Errorf("want %s, got %+v", HintTmuxCreateSessionExists, codes(hs))
	}
	// AllowForce must be true (recoverable via --force kills existing session).
	for _, h := range hs {
		if h.Code == HintTmuxCreateSessionExists && !h.AllowForce {
			t.Error("session-exists must have AllowForce=true")
		}
	}
}

func Test_tmuxCreateHints_notAWorktree(t *testing.T) {
	state := &MockState{GitWorktrees: map[string]bool{}} // /w is not a worktree
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if !containsCode(hs, HintTmuxCreateNotAWorktree) {
		t.Errorf("want %s, got %+v", HintTmuxCreateNotAWorktree, codes(hs))
	}
	for _, h := range hs {
		if h.Code == HintTmuxCreateNotAWorktree && h.AllowForce {
			t.Error("not-a-worktree must have AllowForce=false (principled refusal)")
		}
	}
}

// When --cwd is not a worktree, downstream identity checks must not run
// (no point asking about identity in a non-worktree).
func Test_tmuxCreateHints_notAWorktree_shortCircuits(t *testing.T) {
	state := &MockState{
		GitWorktrees: map[string]bool{},
		IdentityStatuses: map[string]MockIdentity{
			"/w": {Status: IdentityLive, Agent: &AgentSummary{AgentID: "ghost"}},
		},
	}
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if containsCode(hs, HintTmuxCreateIdentityExistsAlive) {
		t.Error("identity-exists checks must not fire when path is not a worktree")
	}
}

func Test_tmuxCreateHints_identityExistsAlive(t *testing.T) {
	state := &MockState{
		GitWorktrees: map[string]bool{"/w": true},
		IdentityStatuses: map[string]MockIdentity{
			"/w": {Status: IdentityLive, Agent: &AgentSummary{AgentID: "agent_foo", TmuxSession: "agent_foo"}},
		},
	}
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if !containsCode(hs, HintTmuxCreateIdentityExistsAlive) {
		t.Errorf("want %s, got %+v", HintTmuxCreateIdentityExistsAlive, codes(hs))
	}
	for _, h := range hs {
		if h.Code == HintTmuxCreateIdentityExistsAlive && h.AllowForce {
			t.Error("identity-exists-alive must have AllowForce=false (principled refusal)")
		}
	}
}

func Test_tmuxCreateHints_identityExistsStale(t *testing.T) {
	state := &MockState{
		GitWorktrees: map[string]bool{"/w": true},
		IdentityStatuses: map[string]MockIdentity{
			"/w": {Status: IdentityStale, Agent: &AgentSummary{AgentID: "agent_foo", TmuxSession: "agent_foo"}},
		},
	}
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if !containsCode(hs, HintTmuxCreateIdentityExistsStale) {
		t.Errorf("want %s, got %+v", HintTmuxCreateIdentityExistsStale, codes(hs))
	}
	for _, h := range hs {
		if h.Code == HintTmuxCreateIdentityExistsStale && !h.AllowForce {
			t.Error("identity-exists-stale must have AllowForce=true")
		}
	}
}

func Test_tmuxCreateHints_identityNone_silentForIdentityFamily(t *testing.T) {
	state := &MockState{
		GitWorktrees:     map[string]bool{"/w": true},
		IdentityStatuses: map[string]MockIdentity{"/w": {Status: IdentityNone}},
	}
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if containsCode(hs, HintTmuxCreateIdentityExistsAlive) ||
		containsCode(hs, HintTmuxCreateIdentityExistsStale) {
		t.Errorf("IdentityNone must emit no identity-exists-* hint, got %+v", codes(hs))
	}
}

func Test_tmuxCreateHints_nextLaunch_postOnly(t *testing.T) {
	state := &MockState{
		GitWorktrees:     map[string]bool{"/w": true},
		IdentityStatuses: map[string]MockIdentity{"/w": {Status: IdentityNone}},
	}
	if hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state)); containsCode(hs, HintTmuxCreateNextLaunch) {
		t.Error("next-launch must NOT fire in pre-action phase")
	}
	if hs := tmuxCreateHints(tmuxCtx(true, "/w", false, state)); !containsCode(hs, HintTmuxCreateNextLaunch) {
		t.Errorf("next-launch must fire in post-action, got %+v", codes(hs))
	}
}

func Test_tmuxCreateHints_identityReplaced_requiresForceAndStaleMarker(t *testing.T) {
	state := &MockState{
		GitWorktrees:     map[string]bool{"/w": true},
		IdentityStatuses: map[string]MockIdentity{"/w": {Status: IdentityNone}}, // post-replacement state
	}

	// Positive: post + force + Result marker flagged.
	ctx := tmuxCtx(true, "/w", true, state)
	ctx.Result = TmuxCreateResultMarker{ReplacedStaleIdentity: true, ReplacedAgentName: "agent_foo"}
	hs := tmuxCreateHints(ctx)
	if !containsCode(hs, HintTmuxCreateIdentityReplaced) {
		t.Errorf("want %s, got %+v", HintTmuxCreateIdentityReplaced, codes(hs))
	}

	// Without --force: nothing to replace, no audit hint.
	ctx = tmuxCtx(true, "/w", false, state)
	ctx.Result = TmuxCreateResultMarker{ReplacedStaleIdentity: true, ReplacedAgentName: "agent_foo"}
	if hs := tmuxCreateHints(ctx); containsCode(hs, HintTmuxCreateIdentityReplaced) {
		t.Error("identity-replaced must not fire without --force")
	}

	// With --force but marker absent (e.g. force on fresh worktree): no audit hint.
	ctx = tmuxCtx(true, "/w", true, state)
	ctx.Result = TmuxCreateResultMarker{} // explicitly unset
	if hs := tmuxCreateHints(ctx); containsCode(hs, HintTmuxCreateIdentityReplaced) {
		t.Error("identity-replaced must not fire when Result marker is unset")
	}
}

func Test_tmuxCreateHints_silentWhenAllOK(t *testing.T) {
	state := &MockState{
		GitWorktrees:     map[string]bool{"/w": true},
		IdentityStatuses: map[string]MockIdentity{"/w": {Status: IdentityNone}},
		TmuxSessions:     map[string]bool{},
	}
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if len(hs) != 0 {
		t.Errorf("expected silent pre-action when nothing wrong, got %+v", codes(hs))
	}
}

// Error paths on StateAccessor must be silent (best-effort).
func Test_tmuxCreateHints_stateAccessorError_silent(t *testing.T) {
	// IsGitWorktree errors — must not emit not-a-worktree.
	state := &MockState{Errs: MockErrs{IsGitWorktree: errBoom}}
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if containsCode(hs, HintTmuxCreateNotAWorktree) {
		t.Error("IsGitWorktree error must not fire not-a-worktree (best-effort)")
	}
}

// TmuxSessionExists error must not fire session-exists.
func Test_tmuxCreateHints_tmuxSessionExistsError_silent(t *testing.T) {
	state := &MockState{
		GitWorktrees:     map[string]bool{"/w": true},
		IdentityStatuses: map[string]MockIdentity{"/w": {Status: IdentityNone}},
		Errs:             MockErrs{TmuxSessionExists: errBoom},
	}
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if containsCode(hs, HintTmuxCreateSessionExists) {
		t.Error("TmuxSessionExists error must not fire session-exists (best-effort)")
	}
}

// IdentityStatus error must not fire either identity-exists-* hint.
func Test_tmuxCreateHints_identityStatusError_silent(t *testing.T) {
	state := &MockState{
		GitWorktrees: map[string]bool{"/w": true},
		Errs:         MockErrs{IdentityStatus: errBoom},
	}
	hs := tmuxCreateHints(tmuxCtx(false, "/w", false, state))
	if containsCode(hs, HintTmuxCreateIdentityExistsAlive) ||
		containsCode(hs, HintTmuxCreateIdentityExistsStale) {
		t.Errorf("IdentityStatus error must not fire identity-exists-* (best-effort), got %+v", codes(hs))
	}
}

// nil ctx.State must not panic; all state-dependent checks skip silently.
func Test_tmuxCreateHints_nilState_silent(t *testing.T) {
	ctx := HintCtx{
		Command: "tmux.create",
		Args:    []string{"foo"},
		Flags:   map[string]any{"cwd": "/w", "force": false},
		Post:    false,
		State:   nil,
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil State panicked: %v", r)
		}
	}()
	hs := tmuxCreateHints(ctx)
	if len(hs) != 0 {
		t.Errorf("nil State: expected zero hints, got %+v", codes(hs))
	}
}

// Helpers.
func containsCode(hs []Hint, code string) bool {
	for _, h := range hs {
		if h.Code == code {
			return true
		}
	}
	return false
}
func codes(hs []Hint) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Code)
	}
	return out
}

var errBoom = &boomErr{msg: "boom"}

type boomErr struct{ msg string }

func (e *boomErr) Error() string { return e.msg }
