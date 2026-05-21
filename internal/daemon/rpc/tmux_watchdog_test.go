package rpc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/identitybanner"
)

// buildClaudeFreshPane returns a simulated pane snapshot that mimics a freshly
// launched Claude Code agent: banner sentinel, blank lines, animated spinner,
// more blank lines, and the horizontal rule separating transcript from chrome.
// No real agent output is present — the watchdog should fire a nudge.
func buildClaudeFreshPane() string {
	return "Agent: @test-agent\nRole:  implementer\n" +
		identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"\n" +
		"✻ Churned for 3s\n" +
		"\n" +
		"\n" +
		"────────────────────────────────────────\n" +
		"│ >                                     │\n" +
		"claude-sonnet-4-6 · 0 tokens · ~/dev\n" +
		"? for shortcuts\n"
}

// buildClaudeRespondedPane returns a pane where the agent has written a line
// after the sentinel — the watchdog must NOT fire a nudge.
func buildClaudeRespondedPane() string {
	return "Agent: @test-agent\n" +
		identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"I'll read the prime now.\n" +
		"\n" +
		"✻ Scanning for 5s\n" +
		"────────────────────────────────────────\n" +
		"│ >                                     │\n"
}

var (
	testBottomAnchorRe = regexp.MustCompile(`^─{20,}$`)
	// testSpinnerRe uses \S+ (not \w+) to handle unicode verb suffixes like
	// "Sautéed", and accepts both short ("for 17s") and long ("for 1m 45s")
	// duration formats. Mirrors claudeSpinnerRegex in internal/runtime/presets.go.
	testSpinnerRe = regexp.MustCompile(`^✻ \S+ for \d+(?:m \d+)?s$`)
)

// ── paneAgentEngaged unit tests ──────────────────────────────────────────────

// TestPaneAgentEngaged_FreshPane: banner + blanks + spinner + rule → not engaged → nudge.
func TestPaneAgentEngaged_FreshPane(t *testing.T) {
	got := paneAgentEngaged(buildClaudeFreshPane(), testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) for fresh pane with only spinner between anchors")
	}
}

// TestPaneAgentEngaged_AgentResponded: agent wrote text → engaged → no nudge.
func TestPaneAgentEngaged_AgentResponded(t *testing.T) {
	got := paneAgentEngaged(buildClaudeRespondedPane(), testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (engaged) when agent output is present between anchors")
	}
}

// TestPaneAgentEngaged_NoTopAnchor: sentinel scrolled out → conservative true.
func TestPaneAgentEngaged_NoTopAnchor(t *testing.T) {
	pane := "✻ Scanning for 2s\n────────────────────────────────────────\n│ > │\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (conservative) when top anchor is missing")
	}
}

// TestPaneAgentEngaged_NoAnchorsAtAll: both bottom anchor and spinner regexes
// nil → no way to bound the decision region → conservative true.
func TestPaneAgentEngaged_NoAnchorsAtAll(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n\nSomething\n\n"
	got := paneAgentEngaged(pane, nil, nil)
	if !got {
		t.Error("expected true (conservative) when both bottom anchor and spinner regexes are nil")
	}
}

// TestPaneAgentEngaged_TipBelowSpinner_NotEngaged pins the rc.3 fix: Claude
// renders footer-region tip lines (e.g. "tmux focus-events off · add ...") in
// the band between the spinner and the divider. The OLD algorithm walked the
// entire (sentinel..divider) region and treated those tips as real agent output
// → false-positive engaged → no nudge. The NEW algorithm uses the spinner as
// the bottom anchor, so tips below the spinner are out of scope.
func TestPaneAgentEngaged_TipBelowSpinner_NotEngaged(t *testing.T) {
	pane := "Agent: @test-agent\n" +
		identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"\n" +
		"✻ Cogitated for 3s\n" +
		"\n\n\n\n\n\n\n\n\n\n\n\n\n" +
		"tmux focus-events off · add 'set -g focus-events on' to ~/.tmux.conf and reattach for focus tracking\n" +
		"────────────────────────────────────────────────────────────────────────────────\n" +
		"❯\n" +
		"  Model: Opus 4.7 | Ctx: 34.7k | Sessio...\n" +
		"  ⏵⏵ accept edits on (shift+tab to cycle)\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) when only blanks between sentinel and spinner — tip below spinner must not count as agent output")
	}
}

// TestPaneAgentEngaged_NoSpinnerYet_FallsBackToDivider: brief window after
// banner injection but before the runtime has rendered any spinner. The
// algorithm falls back to the divider as the bottom anchor.
func TestPaneAgentEngaged_NoSpinnerYet_FallsBackToDivider(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n\n\n────────────────────────────────────────\n│ > │\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) when no spinner yet and only blanks between sentinel and divider")
	}
}

// TestPaneAgentEngaged_NoSpinnerYet_AgentRespondedFast: agent responded
// before the spinner had a chance to render — divider fallback still
// detects the response.
func TestPaneAgentEngaged_NoSpinnerYet_AgentRespondedFast(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n\nReady.\n\n────────────────────────────────────────\n│ > │\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (engaged) when agent responded before any spinner render")
	}
}

// TestPaneAgentEngaged_BottomAboveTop: rule above sentinel → conservative true.
func TestPaneAgentEngaged_BottomAboveTop(t *testing.T) {
	pane := "────────────────────────────────────────\n" + identitybanner.PrimeTruncationSentinel + "\n\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (conservative) when bottom anchor is above top anchor")
	}
}

// TestPaneAgentEngaged_NilSpinnerRe_BlanksOnly: no spinner re, only blanks → not engaged.
func TestPaneAgentEngaged_NilSpinnerRe_BlanksOnly(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n\n\n────────────────────────────────────────\n│ > │\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, nil)
	if got {
		t.Error("expected false (not engaged) with nil spinnerRe and only blanks between anchors")
	}
}

// TestPaneAgentEngaged_NilSpinnerRe_AnyText: no spinner re, non-blank text → engaged.
func TestPaneAgentEngaged_NilSpinnerRe_AnyText(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\nSome output\n────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, nil)
	if !got {
		t.Error("expected true (engaged) with nil spinnerRe and text between anchors")
	}
}

// TestPaneAgentEngaged_MultipleSpinnerLines: multiple consecutive spinner lines
// between anchors → not engaged (all are chrome).
func TestPaneAgentEngaged_MultipleSpinnerLines(t *testing.T) {
	// Use verbs that the regex matches (including unicode with \S+)
	pane := identitybanner.PrimeTruncationSentinel +
		"\n✻ Sautéed for 1s\n✻ Churned for 2s\n✻ Simmered for 3s\n" +
		"────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) when only multiple spinner lines between anchors")
	}
}

// buildClaude2_1_141FreshPane simulates the Claude Code v2.1.141 welcome
// layout AFTER thrum-8dl3 Fix #1 lands (identity banner emitted pre-launch,
// so its sentinel sits in scrollback above the alt-screen). The 2.1.141
// input box is bracketed by TWO horizontal-rule lines (`─{20,}`) — the
// bottom-anchor walk must lock onto the FIRST one (top rule), not the
// global last, otherwise the top rule falls INSIDE the walk window and
// gets trimmed to non-empty → false-engaged → no nudge. Fix #3 pins this.
func buildClaude2_1_141FreshPane() string {
	return identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"\n" +
		"✻ Churned for 3s\n" +
		"\n" +
		"\n" +
		"────────────────────────────────────────\n" + // TOP rule of input box
		"❯                                       \n" +
		"────────────────────────────────────────\n" + // BOTTOM rule of input box
		"  Model: Opus 4.7 | Ctx: 0 | ~/dev\n" +
		"  ⏵⏵ accept edits on (shift+tab to cycle)\n"
}

// buildClaude2_1_141RespondedPane is the with-content twin of
// buildClaude2_1_141FreshPane: agent has produced prior-turn content above
// the spinner, so the walk window legitimately contains non-blank lines.
func buildClaude2_1_141RespondedPane() string {
	return identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"I'll read the prime now.\n" +
		"\n" +
		"✻ Cooked for 42s\n" +
		"\n" +
		"\n" +
		"────────────────────────────────────────\n" +
		"❯                                       \n" +
		"────────────────────────────────────────\n" +
		"  Model: Opus 4.7 | Ctx: 0.5k | ~/dev\n"
}

// TestPaneAgentEngaged_Claude2_1_141_FreshPane_NotEngaged pins thrum-8dl3
// Fix #3: the bottom-anchor walk locks onto the FIRST `─{20,}` match after
// the top anchor (the top rule of the input box), NOT the last (the bottom
// rule). Walking past the top rule into the input-box interior would count
// the top rule itself as "real agent output" and suppress the nudge.
func TestPaneAgentEngaged_Claude2_1_141_FreshPane_NotEngaged(t *testing.T) {
	got := paneAgentEngaged(buildClaude2_1_141FreshPane(), testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) for fresh 2.1.141 pane — bottom-anchor walk must stop at the FIRST horizontal rule after the top anchor, not include the input-box interior")
	}
}

// TestPaneAgentEngaged_Claude2_1_141_RespondedPane_Engaged is the positive
// counterpart: real agent content above the spinner must keep the watchdog
// from firing.
func TestPaneAgentEngaged_Claude2_1_141_RespondedPane_Engaged(t *testing.T) {
	got := paneAgentEngaged(buildClaude2_1_141RespondedPane(), testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (engaged) when agent prior-turn content sits between sentinel and spinner on a 2.1.141 pane")
	}
}

// TestPaneAgentEngaged_Claude2_1_141_LongFormSpinner_NotEngaged is the
// integration counterpart to TestClaudeSpinnerRegex_MatchesAllObservedFormats:
// the regex itself accepts the multi-minute form, but if a future refactor
// negates spinner detection inside paneAgentEngaged the isolated regex test
// would still pass while the watchdog regresses silently — the exact shape
// of the original thrum-8dl3 bug. This test wires the full path: feed a
// synthetic 2.1.141 pane whose only "agent output" candidate is a long-form
// spinner (`✻ Baked for 1m 45s`) and assert paneAgentEngaged returns false
// (not engaged → watchdog should nudge).
func TestPaneAgentEngaged_Claude2_1_141_LongFormSpinner_NotEngaged(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"\n" +
		"✻ Baked for 1m 45s\n" + // long-form duration past 60s
		"\n" +
		"\n" +
		"────────────────────────────────────────\n" +
		"❯                                       \n" +
		"────────────────────────────────────────\n" +
		"  Model: Opus 4.7 | Ctx: 0 | ~/dev\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) when only a long-form spinner (`✻ Baked for 1m 45s`) sits between sentinel and divider — Fix #2's regex extension must propagate to the watchdog's engagement check")
	}
}

// TestPaneAgentEngaged_Claude2_1_141_NoSpinnerYet_NotEngaged covers the
// brief window after pre-launch banner injection where the runtime has
// rendered the welcome chrome (two horizontal rules) but hasn't started a
// turn yet (no spinner line). The divider-fallback path must still pick
// the TOP rule as bottomIdx so the input-box interior stays out of scope.
func TestPaneAgentEngaged_Claude2_1_141_NoSpinnerYet_NotEngaged(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"\n" +
		"────────────────────────────────────────\n" + // TOP rule
		"❯                                       \n" +
		"────────────────────────────────────────\n" + // BOTTOM rule
		"  Model: Opus 4.7 | Ctx: 0 | ~/dev\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) for 2.1.141 fresh pane with no spinner yet — divider fallback must pick the FIRST rule (top of input box), not the last")
	}
}

// TestPaneAgentEngaged_MultiLineResponse: multi-line agent prose → engaged.
func TestPaneAgentEngaged_MultiLineResponse(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel +
		"\nI will now read the prime.\nLet me check the context.\n✻ Scanning for 2s\n" +
		"────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (engaged) for multi-line agent response")
	}
}

// TestPaneAgentEngaged_AckLineOnly_NotEngaged pins thrum-qpw7: an agent that
// prints the one-line ack mandated by the identity printf (`@<name> primed
// (...). Standing by.`) but takes no further action — most importantly does
// NOT invoke Read on the (potentially truncated) prime briefing — must not
// false-positive as engaged. The old algorithm walked sentinel..spinner and
// treated the ack line as "real agent output" → no nudge → agent stuck
// operating on truncated context. The fix excludes ack-pattern lines from the
// non-blank check so the corrective nudge fires.
func TestPaneAgentEngaged_AckLineOnly_NotEngaged(t *testing.T) {
	pane := "Agent: @test-agent\n" +
		identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"@test-agent primed (implementer/test-branch). Standing by for next dispatch.\n" +
		"\n" +
		"✻ Cooked for 3s\n" +
		"────────────────────────────────────────\n" +
		"❯                                       \n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) when only the ack line + blanks appear between sentinel and spinner — the printf-mandated ack is not evidence the agent read the prime")
	}
}

// TestPaneAgentEngaged_AckLinePlusResponse_Engaged is the positive counterpart
// to AckLineOnly: when the agent both acks AND produces real prose (e.g.
// reporting what it found in the prime), engaged must return true so the
// nudge does NOT fire. This pins the no-double-nudge guarantee from the bug's
// acceptance criteria.
func TestPaneAgentEngaged_AckLinePlusResponse_Engaged(t *testing.T) {
	pane := "Agent: @test-agent\n" +
		identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"@test-agent primed (implementer/test-branch). Reviewing Resume Plan.\n" +
		"Inbox empty; standing by for coord dispatch.\n" +
		"\n" +
		"✻ Cooked for 5s\n" +
		"────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (engaged) when the ack line is followed by real agent prose between sentinel and spinner — only the ack itself is ignored, not legitimate content alongside it")
	}
}

// TestPaneAgentEngaged_AckLineWithBlankPadding_NotEngaged is a positional-
// invariance counterpart to AckLineOnly: the ack line may appear immediately
// after the sentinel, or after multiple blanks, depending on runtime render
// pacing. Either way the algorithm must classify the region as not-engaged.
func TestPaneAgentEngaged_AckLineWithBlankPadding_NotEngaged(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"\n" +
		"\n" +
		"@another-agent primed (researcher/feature/branch-name). Standing by.\n" +
		"\n" +
		"\n" +
		"✻ Churned for 1s\n" +
		"────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) when the ack line is surrounded by blanks regardless of position between sentinel and spinner")
	}
}

// TestPaneAgentEngaged_PrimedProseNotAck_Engaged pins the regex narrowness:
// the printfAckLineRe pattern anchors on the canonical `primed (` opener so
// unrelated prose that happens to use the word "primed" after an @-mention
// (e.g. an agent's status message about a database initialization) is NOT
// mis-classified as an ack and the engagement check correctly returns true.
// Without the literal `(` anchor, a looser pattern would suppress real
// agent output and fire a redundant nudge.
func TestPaneAgentEngaged_PrimedProseNotAck_Engaged(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"@impl_v0105 primed the database with 12 fixtures.\n" +
		"\n" +
		"✻ Cooked for 2s\n" +
		"────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (engaged) — `@<name> primed <prose>` without a literal `(` opener is not an ack and must count as real agent output")
	}
}

// TestPaneAgentEngaged_TwoAckLines_NotEngaged is a defensive case for the
// rare scenario where two ack lines appear in the decision region (e.g. a
// coordinator handoff banner followed by an implementer's own ack injected
// before the agent had a chance to Read the prime). All ack-matching lines
// must be filtered, so the watchdog still fires the corrective nudge.
func TestPaneAgentEngaged_TwoAckLines_NotEngaged(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"@coordinator_main primed (coordinator/main). Standing by.\n" +
		"@impl_v0105 primed (implementer/v0105). Standing by.\n" +
		"\n" +
		"✻ Cooked for 1s\n" +
		"────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) when multiple canonical ack lines appear back-to-back between sentinel and spinner — each must be filtered as not-real-output")
	}
}

// TestPaneAgentEngaged_LongBannerJoinedPostKtp8_NotEngaged pins thrum-ktp8: the
// full identity-banner printf body (Agent/Role/Worktree/Branch lines + the
// must-read sentinel) is long enough that without tmux's `-J` capture flag
// the sentinel wraps mid-string across two pane lines. When that happens,
// `strings.Contains(line, sentinel)` finds NEITHER half → topIdx stays at
// -1 → paneAgentEngaged returns true conservatively → no corrective nudge
// fires → the rc.5 thrum-qpw7 ack-line exclusion never runs (masked bug).
// The fix is to add `-J` to CapturePane (internal/tmux/tmux.go) so tmux
// joins wrapped lines BEFORE returning. This test pins the failure surface:
// when CapturePane returns the realistic full-banner content in JOINED form
// (one logical line per printf arg, sentinel intact), paneAgentEngaged
// correctly walks the region between sentinel and spinner and falls through
// to the ack-exclusion path, returning false (not engaged → nudge fires).
//
// Fixture mirrors the actual pane shape from Leon's rc.5 spot-check of
// @impl_writer_website_dev (bd thrum-ktp8 description) — the live trigger
// case that surfaced the wrap bug.
func TestPaneAgentEngaged_LongBannerJoinedPostKtp8_NotEngaged(t *testing.T) {
	pane := "Agent: @impl_writer_website_dev\n" +
		"Role:  implementer\n" +
		"Worktree: /Users/leon/.thrum/worktrees/thrum/website-dev\n" +
		"Branch: website-dev\n" +
		identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"@impl_writer_website_dev primed (implementer/website-dev). Standing by for website-dev tasks. Standing by.\n" +
		"\n" +
		"✻ Cooked for 3s\n" +
		"\n" +
		"────────────────────────────────────────\n" +
		"❯                                       \n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) for the post-ktp8 joined-form full-banner pane — sentinel must be detected on its own line, decision region must contain only blanks + the canonical ack line, and the watchdog must fire the corrective nudge")
	}
}

// ── nudgeSilentPaneAfter integration tests ───────────────────────────────────

// stableActivity returns a tmuxLastActivityFn stub that always reports
// pane-idle-for-silenceThreshold+1s (i.e. silence immediately detected).
func stableActivity() func(string) (time.Time, error) {
	past := time.Now().Add(-(silenceThreshold + time.Second))
	return func(_ string) (time.Time, error) { return past, nil }
}

// TestNudgeSilentPaneAfter_NudgesWhenSilent pins thrum-puhr.10 + thrum-84xc:
// when the pane shows only spinner/blanks between banner sentinel and horizontal
// rule, the watchdog fires send-keys + Enter with the configured nudge text.
func TestNudgeSilentPaneAfter_NudgesWhenSilent(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	tmuxLastActivityFn = stableActivity()
	capturePaneFn = func(_ string, _ int) (string, error) { return buildClaudeFreshPane(), nil }
	sleepFn = func(time.Duration) {}

	var sent atomic.Value
	sendKeysFn = func(target, text string) error {
		sent.Store(target + "|" + text)
		return nil
	}
	var enter atomic.Bool
	sendSpecialKeyFn = func(_, key string) error {
		if key == "Enter" {
			enter.Store(true)
		}
		return nil
	}

	thrumDir := t.TempDir()
	// Default config (silence_watchdog_seconds == 0) → enabled, 30s.
	nudgeSilentPaneAfter("sess:0.0", "claude", thrumDir, "thrum inbox --unread")

	got := sent.Load()
	if got == nil || got.(string) != "sess:0.0|thrum inbox --unread" {
		t.Errorf("expected send-keys nudge fired with target+text, got %v", got)
	}
	if !enter.Load() {
		t.Error("expected Enter sent after nudge")
	}
}

// TestNudgeSilentPaneAfter_SkipsWhenActive verifies no nudge fires when
// the agent has produced real output between the anchors.
func TestNudgeSilentPaneAfter_SkipsWhenActive(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	tmuxLastActivityFn = stableActivity()
	capturePaneFn = func(_ string, _ int) (string, error) { return buildClaudeRespondedPane(), nil }
	sleepFn = func(time.Duration) {}

	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "claude", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no send-keys when agent has responded; got %d", got)
	}
}

// TestWaitForPaneReady_ReturnsWhenStable verifies the readiness helper
// returns once tmux reports the pane has been silent for at least
// silenceThreshold (5s).
func TestWaitForPaneReady_ReturnsWhenStable(t *testing.T) {
	prevCapture, prevSleep, prevActivity := capturePaneFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	// Pane already silent for > silenceThreshold → readiness fires on first poll.
	tmuxLastActivityFn = stableActivity()
	var captureCount atomic.Int32
	capturePaneFn = func(_ string, _ int) (string, error) {
		captureCount.Add(1)
		return "$ _", nil // idle prompt — safe to type
	}
	sleepFn = func(time.Duration) {}

	if !waitForPaneReady("sess:0.0", "claude", 2, 30) {
		t.Errorf("waitForPaneReady should have reported ready on a silent idle pane")
	}
	// Silence-driven path captures once for the post-settle safety check.
	if got := captureCount.Load(); got != 1 {
		t.Errorf("expected 1 capture (post-settle safety check), got %d", got)
	}
}

// TestWaitForPaneReady_BailsAtCeiling verifies the readiness helper
// gives up after ceiling seconds when the pane never goes silent.
func TestWaitForPaneReady_BailsAtCeiling(t *testing.T) {
	prevCapture, prevSleep, prevActivity, prevNow := capturePaneFn, sleepFn, tmuxLastActivityFn, timeNowFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
		timeNowFn = prevNow
	})

	// Pane is constantly "active" (window_activity == now) — silence never reached.
	// Advance simulated time on each tmuxLastActivityFn call so the deadline fires.
	base := time.Now()
	var ticks atomic.Int32
	timeNowFn = func() time.Time {
		return base.Add(time.Duration(ticks.Load()) * time.Second)
	}
	tmuxLastActivityFn = func(_ string) (time.Time, error) {
		ticks.Add(1)
		return timeNowFn(), nil // never silent
	}
	capturePaneFn = func(_ string, _ int) (string, error) { return "$ _", nil }
	sleepFn = func(time.Duration) {}

	start := time.Now()
	safe := waitForPaneReady("sess:0.0", "claude", 2, 5)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waitForPaneReady should have returned quickly with mocked sleep, took %v", elapsed)
	}
	// Ceiling fires → captures last pane for safety check → returns IsPaneSafeToType result.
	if !safe {
		t.Errorf("waitForPaneReady at ceiling on a safe pane should return true")
	}
}

// TestNudgeSilentPaneAfter_SkipsOnTrustGate pins the cluster-8 guard:
// even if the pane has no agent output, no nudge keystroke fires when
// permission.IsPaneSafeToType reports the pane as unsafe.
func TestNudgeSilentPaneAfter_SkipsOnTrustGate(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	const trust = "Do you trust the contents of this directory?\n  1. Yes\n  2. No"
	tmuxLastActivityFn = stableActivity()
	capturePaneFn = func(_ string, _ int) (string, error) { return trust, nil }
	sleepFn = func(time.Duration) {}

	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "codex", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("watchdog typed into a trust gate; expected 0 send-keys, got %d", got)
	}
}

// TestWaitForPaneReady_ReturnsUnsafeOnTrustGate pins the cluster-8
// readiness-side guard: when the silent pane is at a trust gate the
// function reports !safe so callers skip the inject.
func TestWaitForPaneReady_ReturnsUnsafeOnTrustGate(t *testing.T) {
	prevCapture, prevSleep, prevActivity := capturePaneFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	const trust = "Do you trust the contents of this directory?\n  1. Yes\n  2. No"
	tmuxLastActivityFn = stableActivity()
	capturePaneFn = func(_ string, _ int) (string, error) { return trust, nil }
	sleepFn = func(time.Duration) {}

	safe := waitForPaneReady("sess:0.0", "codex", 2, 30)
	if safe {
		t.Errorf("waitForPaneReady reported safe on a trust gate; expected unsafe")
	}
}

func TestWaitForPaneReady_ReturnsSafeOnIdlePane(t *testing.T) {
	prevCapture, prevSleep, prevActivity := capturePaneFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	tmuxLastActivityFn = stableActivity()
	capturePaneFn = func(_ string, _ int) (string, error) { return "$ _", nil }
	sleepFn = func(time.Duration) {}

	if !waitForPaneReady("sess:0.0", "codex", 2, 30) {
		t.Errorf("waitForPaneReady reported unsafe on idle pane; expected safe")
	}
}

// TestNudgeSilentPaneAfter_DisabledByConfig verifies the watchdog
// short-circuits when restart.silence_watchdog_seconds is negative.
func TestNudgeSilentPaneAfter_DisabledByConfig(t *testing.T) {
	prevCapture, prevSend, prevSleep, prevActivity := capturePaneFn, sendKeysFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	var captureCount atomic.Int32
	capturePaneFn = func(_ string, _ int) (string, error) {
		captureCount.Add(1)
		return "", nil
	}
	var activityCount atomic.Int32
	tmuxLastActivityFn = func(_ string) (time.Time, error) {
		activityCount.Add(1)
		return time.Now(), nil
	}
	sleepFn = func(time.Duration) {}
	sendKeysFn = func(_, _ string) error { return nil }

	thrumDir := t.TempDir()
	configJSON := `{"restart":{"silence_watchdog_seconds":-1}}`
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	nudgeSilentPaneAfter("sess:0.0", "claude", thrumDir, "thrum inbox --unread")

	if got := captureCount.Load(); got != 0 {
		t.Errorf("expected no capture calls when watchdog disabled; got %d", got)
	}
	if got := activityCount.Load(); got != 0 {
		t.Errorf("expected no activity calls when watchdog disabled; got %d", got)
	}
}

// TestNudgeSilentPaneAfter_DeadlineExitsWhenBusy verifies that the watchdog
// exits silently (no nudge) when window_activity bumps continuously past the
// 30s deadline without ever achieving silenceThreshold of quiet.
func TestNudgeSilentPaneAfter_DeadlineExitsWhenBusy(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity, prevNow :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn, timeNowFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
		timeNowFn = prevNow
	})

	// Use a fake clock: both timeNowFn and tmuxLastActivityFn use it so
	// silenceFor = timeNowFn() - activity = 0 (activity is always "just now"
	// on the fake clock), and the deadline can be blown by advancing the clock.
	var fakeNow atomic.Value
	fakeNow.Store(time.Now())
	timeNowFn = func() time.Time { return fakeNow.Load().(time.Time) }

	// Activity is always at fakeNow — silence never exceeds silenceThreshold.
	tmuxLastActivityFn = func(_ string) (time.Time, error) { return fakeNow.Load().(time.Time), nil }

	// sleepFn advances the fake clock by 1s so the 30s deadline fires after ~31 ticks.
	sleepFn = func(d time.Duration) {
		fakeNow.Store(fakeNow.Load().(time.Time).Add(time.Second))
	}

	var captureCount atomic.Int32
	capturePaneFn = func(_ string, _ int) (string, error) {
		captureCount.Add(1)
		return "some pane", nil
	}
	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "claude", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no nudge when busy past deadline; got %d send-keys calls", got)
	}
	if got := captureCount.Load(); got != 0 {
		t.Errorf("expected no capture when busy past deadline; got %d", got)
	}
}

// TestNudgeSilentPaneAfter_TransientActivityErrors verifies the watchdog
// tolerates up to watchdogMaxConsecutiveErrors-1 errors then succeeds.
func TestNudgeSilentPaneAfter_TransientActivityErrors(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	// First watchdogMaxConsecutiveErrors-1 calls error, then always returns silent.
	var actCall atomic.Int32
	past := time.Now().Add(-(silenceThreshold + time.Second))
	tmuxLastActivityFn = func(_ string) (time.Time, error) {
		n := actCall.Add(1)
		if n <= int32(watchdogMaxConsecutiveErrors-1) {
			return time.Time{}, fmt.Errorf("transient error %d", n)
		}
		return past, nil
	}
	capturePaneFn = func(_ string, _ int) (string, error) { return buildClaudeFreshPane(), nil }
	sleepFn = func(time.Duration) {}

	var sent atomic.Value
	sendKeysFn = func(target, text string) error {
		sent.Store(target + "|" + text)
		return nil
	}
	var enter atomic.Bool
	sendSpecialKeyFn = func(_, key string) error {
		if key == "Enter" {
			enter.Store(true)
		}
		return nil
	}

	nudgeSilentPaneAfter("sess:0.0", "claude", t.TempDir(), "thrum inbox --unread")

	got := sent.Load()
	if got == nil || got.(string) != "sess:0.0|thrum inbox --unread" {
		t.Errorf("expected nudge after transient errors recovered, got %v", got)
	}
}

// TestNudgeSilentPaneAfter_PersistentActivityErrors verifies the watchdog
// exits without nudging when activity errors persist past the threshold.
func TestNudgeSilentPaneAfter_PersistentActivityErrors(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	tmuxLastActivityFn = func(_ string) (time.Time, error) {
		return time.Time{}, fmt.Errorf("target gone")
	}
	capturePaneFn = func(_ string, _ int) (string, error) { return "", nil }
	sleepFn = func(time.Duration) {}

	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "claude", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no nudge when activity errors persist; got %d", got)
	}
}

// TestNudgeSilentPaneAfter_UnknownRuntime verifies watchdog degrades gracefully
// for a runtime with no preset (no anchor/spinner regexes). The conservative
// paneAgentEngaged default (no bottom anchor → true → no nudge) applies.
func TestNudgeSilentPaneAfter_UnknownRuntime(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	// Fresh pane but from an unknown runtime — no regexes → conservative, no nudge.
	tmuxLastActivityFn = stableActivity()
	capturePaneFn = func(_ string, _ int) (string, error) { return buildClaudeFreshPane(), nil }
	sleepFn = func(time.Duration) {}

	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "unknown-runtime-xyz", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no nudge for unknown runtime (conservative); got %d send-keys calls", got)
	}
}
