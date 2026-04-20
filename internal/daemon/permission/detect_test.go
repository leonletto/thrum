package permission

import "testing"

func TestDetectPaneState_CursorMatch(t *testing.T) {
	pane := "Run this command?\nNot in allowlist: curl\n → Run (once) (y)\n"
	got := DetectPaneState("cursor", pane)
	want := "permission:cursor.not_in_allowlist"
	if got != want {
		t.Errorf("DetectPaneState = %q, want %q", got, want)
	}
}

func TestDetectPaneState_ClaudeMatch(t *testing.T) {
	// Full Variant A option list — the tightened tool_confirmation
	// regex (thrum-48kt.7) requires a structural marker alongside
	// "Do you want to proceed?" so conversational prose ending with
	// the same question doesn't match. Option 3's distinctive
	// "No, and tell Claude what to do differently" text is the
	// marker here.
	pane := "⏺ Bash(curl)\n  ⎿  Do you want to proceed?\n     1. Yes\n     2. Yes, and don't ask again for Bash(curl)\n     3. No, and tell Claude what to do differently (Esc)\n"
	got := DetectPaneState("claude", pane)
	want := "permission:claude.tool_confirmation"
	if got != want {
		t.Errorf("DetectPaneState = %q, want %q", got, want)
	}
}

func TestDetectPaneState_AuggieIndexing(t *testing.T) {
	pane := "Choose an option:\n\n   → [1] Always index this workspace - Unlock full workspace understanding"
	got := DetectPaneState("auggie", pane)
	want := "permission:auggie.indexing_consent"
	if got != want {
		t.Errorf("DetectPaneState = %q, want %q", got, want)
	}
}

func TestDetectPaneState_Idle(t *testing.T) {
	if got := DetectPaneState("cursor", "agent> ready"); got != "" {
		t.Errorf("DetectPaneState = %q, want empty", got)
	}
}

func TestDetectPaneState_UnknownRuntime(t *testing.T) {
	if got := DetectPaneState("frobnicator", "anything"); got != "" {
		t.Errorf("DetectPaneState = %q, want empty", got)
	}
}

func TestDetectPaneState_EmptyInput(t *testing.T) {
	if got := DetectPaneState("cursor", ""); got != "" {
		t.Errorf("DetectPaneState(empty pane) = %q, want empty", got)
	}
	if got := DetectPaneState("", "any"); got != "" {
		t.Errorf("DetectPaneState(empty runtime) = %q, want empty", got)
	}
}
