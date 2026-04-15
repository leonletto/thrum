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
	pane := "⏺ Bash(curl)\n  ⎿  Do you want to proceed?\n     1. Yes\n"
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
