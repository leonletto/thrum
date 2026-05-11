package permission

import "testing"

// Representative captured pane samples from the cluster-8 evidence
// (codex + claude first-launch trust dialogs). Kept inline so the
// detector tests are self-contained and survive sample-file churn.

const codexTrustPane = `OpenAI Codex 0.130.0

Do you trust the contents of this directory?

  1. Yes, proceed
  2. No, exit

>`

const claudeTrustPane = `Claude Code

> Quick safety check

Do you trust this folder?

  1. Yes, proceed
  2. No, exit

>`

const welcomeScreenPane = `Codex CLI v0.130.0

Type a message and press Enter to start.

> `

const claudeIdlePane = `claude > _`

// codexTrustPaneRealisticTail mirrors what `tmux capture-pane` returns
// for a fresh codex session sitting at its first-launch trust prompt:
// the dialog renders in the TOP ~10 lines of a 24-line pane, leaving
// blank trailing lines below. Pins cluster-9: IsTrustGate must match
// the full pane, not the bottomLines() window designed for the
// scroll-up permission-prompt path.
const codexTrustPaneRealisticTail = `> You are in /private/tmp/wdtest-x

Do you trust the contents of this directory?

Working with untrusted contents comes with higher risk of prompt
injection. Trusting the directory allows project-local config, hooks,
and exec policies to load.

› 1. Yes, continue
  2. No, quit

Press enter to continue

`

// claudeTrustPaneRealisticTail mirrors the same shape for claude.
const claudeTrustPaneRealisticTail = `Claude Code

> Quick safety check

Do you trust this folder?

  1. Yes, proceed
  2. No, exit

Press enter to continue

`

// TestIsTrustGate_DetectsAtTopOfPaneWithBlankTail pins cluster-9:
// IsTrustGate must NOT consult bottomLines() — that window was
// designed for OnDetection's scroll-up case and silently truncates
// trust prompts that render at the TOP of a fresh pane.
func TestIsTrustGate_DetectsAtTopOfPaneWithBlankTail(t *testing.T) {
	if !IsTrustGate("codex", codexTrustPaneRealisticTail) {
		t.Errorf("codex trust prompt at top of pane with blank tail not detected")
	}
	if !IsTrustGate("claude", claudeTrustPaneRealisticTail) {
		t.Errorf("claude trust prompt at top of pane with blank tail not detected")
	}
	// Generic detector (no runtime hint) must also see it.
	if !IsTrustGate("", codexTrustPaneRealisticTail) {
		t.Errorf("generic detector missed codex trust prompt at top of pane")
	}
	if !IsTrustGate("", claudeTrustPaneRealisticTail) {
		t.Errorf("generic detector missed claude trust prompt at top of pane")
	}
}

func TestIsTrustGate_CodexExact(t *testing.T) {
	if !IsTrustGate("codex", codexTrustPane) {
		t.Errorf("expected IsTrustGate true for codex trust dialog, got false")
	}
}

func TestIsTrustGate_ClaudeExact(t *testing.T) {
	if !IsTrustGate("claude", claudeTrustPane) {
		t.Errorf("expected IsTrustGate true for claude trust dialog, got false")
	}
}

// Generic pattern: even without runtime hint, 1.Yes + 2.No + "trust"
// in the same window flips the detector. Durable against UI text drift.
func TestIsTrustGate_GenericNoRuntimeHint(t *testing.T) {
	if !IsTrustGate("", codexTrustPane) {
		t.Errorf("expected generic IsTrustGate true on codex pane with empty runtime")
	}
	if !IsTrustGate("", claudeTrustPane) {
		t.Errorf("expected generic IsTrustGate true on claude pane with empty runtime")
	}
}

// Welcome screen (safe to type — no trust dialog) must NOT trigger
// the detector. Positive regression guard against the readiness path
// being suppressed forever after the first capture.
func TestIsTrustGate_WelcomeIsNotATrustGate(t *testing.T) {
	if IsTrustGate("codex", welcomeScreenPane) {
		t.Errorf("welcome screen erroneously flagged as trust gate")
	}
	if IsTrustGate("claude", claudeIdlePane) {
		t.Errorf("claude idle prompt erroneously flagged as trust gate")
	}
}

func TestIsTrustGate_EmptyPaneIsNotATrustGate(t *testing.T) {
	if IsTrustGate("codex", "") {
		t.Errorf("empty pane erroneously flagged as trust gate")
	}
}

// Non-trust panel that happens to contain "1. Yes / 2. No" but no
// trust phrasing must NOT match the generic regex — the proximity
// constraint with "trust" is what keeps the detector specific.
func TestIsTrustGate_OptionsWithoutTrustWordIsNotATrustGate(t *testing.T) {
	pane := `Some prompt?
  1. Yes
  2. No
`
	if IsTrustGate("", pane) {
		t.Errorf("non-trust prompt with 1.Yes/2.No options erroneously flagged as trust gate")
	}
}

// IsPaneSafeToType is the chokepoint the four injection sites consult.
// It must return false when either a permission prompt OR a trust gate
// is present, true otherwise.
func TestIsPaneSafeToType_TrustGateBlocks(t *testing.T) {
	if IsPaneSafeToType("codex", codexTrustPane) {
		t.Errorf("expected unsafe for codex trust gate, got safe")
	}
	if IsPaneSafeToType("claude", claudeTrustPane) {
		t.Errorf("expected unsafe for claude trust gate, got safe")
	}
}

func TestIsPaneSafeToType_PermissionPromptBlocks(t *testing.T) {
	pane := "⏺ Bash(curl)\n  ⎿  Do you want to proceed?\n     1. Yes\n     2. Yes, and don't ask again for Bash(curl)\n     3. No, and tell Claude what to do differently (Esc)\n"
	if IsPaneSafeToType("claude", pane) {
		t.Errorf("expected unsafe for claude tool_confirmation, got safe")
	}
}

func TestIsPaneSafeToType_WelcomeScreenIsSafe(t *testing.T) {
	if !IsPaneSafeToType("codex", welcomeScreenPane) {
		t.Errorf("expected safe for codex welcome screen, got unsafe")
	}
	if !IsPaneSafeToType("claude", claudeIdlePane) {
		t.Errorf("expected safe for claude idle prompt, got unsafe")
	}
}
