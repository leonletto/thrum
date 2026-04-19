package cli

import "testing"

func Test_initHints_preAction_silent(t *testing.T) {
	ctx := HintCtx{Command: "init", Post: false, State: &MockState{}}
	if hs := initHints(ctx); len(hs) != 0 {
		t.Errorf("init has no pre-action hints in pilot, got %+v", codes(hs))
	}
}

func Test_initHints_postAction_noIdentity_fires(t *testing.T) {
	state := &MockState{IdentityStatuses: map[string]MockIdentity{"/repo": {Status: IdentityNone}}}
	ctx := HintCtx{
		Command: "init",
		Post:    true,
		Flags:   map[string]any{"repo": "/repo"},
		State:   state,
	}
	hs := initHints(ctx)
	if !containsCode(hs, HintInitNextQuickstart) {
		t.Errorf("want %s, got %+v", HintInitNextQuickstart, codes(hs))
	}
}

func Test_initHints_postAction_identityAlive_silent(t *testing.T) {
	state := &MockState{IdentityStatuses: map[string]MockIdentity{
		"/repo": {Status: IdentityLive, Agent: &AgentSummary{AgentID: "agent_foo", TmuxSession: "agent_foo"}},
	}}
	ctx := HintCtx{
		Command: "init",
		Post:    true,
		Flags:   map[string]any{"repo": "/repo"},
		State:   state,
	}
	if hs := initHints(ctx); containsCode(hs, HintInitNextQuickstart) {
		t.Error("next-quickstart must not fire when identity already exists (Live)")
	}
}

func Test_initHints_postAction_identityStale_silent(t *testing.T) {
	state := &MockState{IdentityStatuses: map[string]MockIdentity{
		"/repo": {Status: IdentityStale, Agent: &AgentSummary{AgentID: "agent_foo"}},
	}}
	ctx := HintCtx{
		Command: "init",
		Post:    true,
		Flags:   map[string]any{"repo": "/repo"},
		State:   state,
	}
	if hs := initHints(ctx); containsCode(hs, HintInitNextQuickstart) {
		t.Error("next-quickstart must not fire when a stale identity exists (still 'has identity')")
	}
}

func Test_initHints_postAction_noRepo_silent(t *testing.T) {
	ctx := HintCtx{Command: "init", Post: true, Flags: map[string]any{}, State: &MockState{}}
	if hs := initHints(ctx); len(hs) != 0 {
		t.Errorf("no --repo must be silent, got %+v", codes(hs))
	}
}

func Test_initHints_stateAccessorError_silent(t *testing.T) {
	state := &MockState{Errs: MockErrs{IdentityStatus: errBoom}}
	ctx := HintCtx{
		Command: "init",
		Post:    true,
		Flags:   map[string]any{"repo": "/repo"},
		State:   state,
	}
	if hs := initHints(ctx); len(hs) != 0 {
		t.Errorf("IdentityStatus error must be silent, got %+v", codes(hs))
	}
}
