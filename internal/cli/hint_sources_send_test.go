package cli

import (
	"testing"
	"time"
)

func Test_sendHints_recipientFresh_silent(t *testing.T) {
	state := &MockState{Agents: map[string]*AgentSummary{
		"target": {AgentID: "target", UpdatedAt: time.Now().Add(-5 * time.Minute).Format(time.RFC3339)},
	}}
	ctx := HintCtx{Command: "send", Flags: map[string]any{"to": "@target"}, State: state}
	if hs := sendHints(ctx); len(hs) != 0 {
		t.Errorf("fresh recipient must be silent, got %+v", codes(hs))
	}
}

func Test_sendHints_recipientStale_fires(t *testing.T) {
	state := &MockState{Agents: map[string]*AgentSummary{
		"target": {AgentID: "target", UpdatedAt: time.Now().Add(-47 * time.Minute).Format(time.RFC3339)},
	}}
	ctx := HintCtx{Command: "send", Flags: map[string]any{"to": "@target"}, State: state}
	hs := sendHints(ctx)
	if !containsCode(hs, HintSendRecipientStale) {
		t.Errorf("stale recipient must fire %s, got %+v", HintSendRecipientStale, codes(hs))
	}
}

// Just under the threshold must be silent. Subtracting 1 minute avoids
// the nanosecond-drift race of testing the exact boundary.
func Test_sendHints_justUnderThreshold_silent(t *testing.T) {
	state := &MockState{Agents: map[string]*AgentSummary{
		"target": {AgentID: "target", UpdatedAt: time.Now().Add(-(RecipientStaleThreshold - time.Minute)).Format(time.RFC3339)},
	}}
	ctx := HintCtx{Command: "send", Flags: map[string]any{"to": "@target"}, State: state}
	if hs := sendHints(ctx); len(hs) != 0 {
		t.Errorf("just-under-threshold recipient must be silent, got %+v", codes(hs))
	}
}

// Well past the threshold fires.
func Test_sendHints_wellPastThreshold_fires(t *testing.T) {
	state := &MockState{Agents: map[string]*AgentSummary{
		"target": {AgentID: "target", UpdatedAt: time.Now().Add(-(RecipientStaleThreshold + 5*time.Minute)).Format(time.RFC3339)},
	}}
	ctx := HintCtx{Command: "send", Flags: map[string]any{"to": "@target"}, State: state}
	if hs := sendHints(ctx); !containsCode(hs, HintSendRecipientStale) {
		t.Errorf("well-past-threshold must fire, got %+v", codes(hs))
	}
}

func Test_sendHints_noTo_silent(t *testing.T) {
	ctx := HintCtx{Command: "send", Flags: map[string]any{}, State: &MockState{}}
	if hs := sendHints(ctx); len(hs) != 0 {
		t.Errorf("no --to must be silent, got %+v", codes(hs))
	}
}

func Test_sendHints_unknownRecipient_silent(t *testing.T) {
	// Unknown-agent is handled by the error path of cli.Send, not by the
	// hint source.
	ctx := HintCtx{Command: "send", Flags: map[string]any{"to": "@unknown"}, State: &MockState{}}
	if hs := sendHints(ctx); len(hs) != 0 {
		t.Errorf("unknown recipient must be silent (error path owns this), got %+v", codes(hs))
	}
}

// Post-action has no send hints in pilot.
func Test_sendHints_postAction_silent(t *testing.T) {
	state := &MockState{Agents: map[string]*AgentSummary{
		"target": {AgentID: "target", UpdatedAt: time.Now().Add(-47 * time.Minute).Format(time.RFC3339)},
	}}
	ctx := HintCtx{Command: "send", Flags: map[string]any{"to": "@target"}, Post: true, State: state}
	if hs := sendHints(ctx); len(hs) != 0 {
		t.Errorf("post-action send has no hints in pilot, got %+v", codes(hs))
	}
}

// AgentByName error must silently skip.
func Test_sendHints_stateAccessorError_silent(t *testing.T) {
	state := &MockState{Errs: MockErrs{AgentByName: errBoom}}
	ctx := HintCtx{Command: "send", Flags: map[string]any{"to": "@target"}, State: state}
	if hs := sendHints(ctx); len(hs) != 0 {
		t.Errorf("AgentByName error must be silent, got %+v", codes(hs))
	}
}

// Unparseable UpdatedAt must silently skip.
func Test_sendHints_unparseableUpdatedAt_silent(t *testing.T) {
	state := &MockState{Agents: map[string]*AgentSummary{
		"target": {AgentID: "target", UpdatedAt: "not a timestamp"},
	}}
	ctx := HintCtx{Command: "send", Flags: map[string]any{"to": "@target"}, State: state}
	if hs := sendHints(ctx); len(hs) != 0 {
		t.Errorf("unparseable UpdatedAt must be silent, got %+v", codes(hs))
	}
}

// --to without @ prefix should also be handled.
func Test_sendHints_toWithoutAtPrefix_stillWorks(t *testing.T) {
	state := &MockState{Agents: map[string]*AgentSummary{
		"target": {AgentID: "target", UpdatedAt: time.Now().Add(-47 * time.Minute).Format(time.RFC3339)},
	}}
	ctx := HintCtx{Command: "send", Flags: map[string]any{"to": "target"}, State: state}
	if hs := sendHints(ctx); !containsCode(hs, HintSendRecipientStale) {
		t.Errorf("bare-name --to must still fire stale detection, got %+v", codes(hs))
	}
}
