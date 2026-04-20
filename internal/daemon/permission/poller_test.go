package permission

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestPoller_FiresOnStable verifies the core debounce behavior: fire
// onStable exactly once when hash stabilizes for stabilityCount
// consecutive polls, then re-arm only when content changes.
func TestPoller_FiresOnStable(t *testing.T) {
	ctx := context.Background()
	var fired []string
	var mu sync.Mutex

	p := NewSessionPoller(SessionPollerConfig{
		CaptureLines:   30,
		StabilityCount: 2,
		Capture: func(target string, lines int) (string, error) {
			return "fixed content", nil
		},
		OnStable: func(ctx context.Context, session, content string) error {
			mu.Lock()
			fired = append(fired, session)
			mu.Unlock()
			return nil
		},
	})

	p.Enroll("sess1", "codex", "sess1:0.0")

	// Poll 1: baseline captured, stableCount=1 (threshold 2 not met)
	p.PollOnce(ctx)
	if len(fired) != 0 {
		t.Fatalf("should not fire on first poll, got %v", fired)
	}

	// Poll 2: hash matches, stableCount=2 == threshold → fires
	p.PollOnce(ctx)
	if len(fired) != 1 || fired[0] != "sess1" {
		t.Fatalf("expected 1 fire for sess1 after threshold, got %v", fired)
	}

	// Poll 3: hash still matches, already fired → does NOT re-fire
	p.PollOnce(ctx)
	if len(fired) != 1 {
		t.Fatalf("should not re-fire on sustained stability, got %v", fired)
	}
}

// TestPoller_ChangingContentNeverFires confirms the poller doesn't fire
// while the pane is still actively changing (agent running, output
// streaming, etc).
func TestPoller_ChangingContentNeverFires(t *testing.T) {
	ctx := context.Background()
	var fired []string
	var mu sync.Mutex
	counter := 0

	p := NewSessionPoller(SessionPollerConfig{
		CaptureLines:   30,
		StabilityCount: 2,
		Capture: func(target string, lines int) (string, error) {
			counter++
			return fmt.Sprintf("content-%d", counter), nil
		},
		OnStable: func(ctx context.Context, session, content string) error {
			mu.Lock()
			fired = append(fired, session)
			mu.Unlock()
			return nil
		},
	})

	p.Enroll("sess1", "codex", "sess1:0.0")

	for i := 0; i < 10; i++ {
		p.PollOnce(ctx)
	}
	if len(fired) != 0 {
		t.Fatalf("should not fire while content keeps changing, got %v", fired)
	}
}

// TestPoller_ReArmsAfterChange verifies onStable fires again after the
// pane becomes stable → changes → becomes stable again.
func TestPoller_ReArmsAfterChange(t *testing.T) {
	ctx := context.Background()
	var fired []string
	var mu sync.Mutex

	phase := "a" // poller steps through phases: stable "a", change, stable "b"
	captureCalls := 0

	p := NewSessionPoller(SessionPollerConfig{
		CaptureLines:   30,
		StabilityCount: 2,
		Capture: func(target string, lines int) (string, error) {
			captureCalls++
			return phase, nil
		},
		OnStable: func(ctx context.Context, session, content string) error {
			mu.Lock()
			fired = append(fired, fmt.Sprintf("%s:%s", session, content))
			mu.Unlock()
			return nil
		},
	})

	p.Enroll("sess1", "codex", "sess1:0.0")

	// Phase A stable: 2 polls with phase="a" → fire "sess1:a"
	p.PollOnce(ctx)
	p.PollOnce(ctx)
	if len(fired) != 1 || fired[0] != "sess1:a" {
		t.Fatalf("phase A: expected [sess1:a], got %v", fired)
	}

	// A third "a" poll does NOT re-fire
	p.PollOnce(ctx)
	if len(fired) != 1 {
		t.Fatalf("phase A sustained: should not re-fire, got %v", fired)
	}

	// Change to phase B — one poll with new content resets stability
	phase = "b"
	p.PollOnce(ctx)
	if len(fired) != 1 {
		t.Fatalf("phase B first: should not fire yet, got %v", fired)
	}

	// Second phase-B poll hits threshold → fires "sess1:b"
	p.PollOnce(ctx)
	if len(fired) != 2 || fired[1] != "sess1:b" {
		t.Fatalf("phase B stable: expected [sess1:a, sess1:b], got %v", fired)
	}
}

// TestPoller_VolatileLinesStripped verifies runtime-specific volatile
// content (codex's "Working (Ns)" timer) is excluded from the hash so
// the poller can detect stability despite the timer ticking.
func TestPoller_VolatileLinesStripped(t *testing.T) {
	ctx := context.Background()
	var fired []string
	var mu sync.Mutex

	// Simulate codex where only the "Working (Ns)" line changes between
	// polls. If volatile-line stripping works, the remaining content is
	// stable and the poller should fire.
	poll := 0
	p := NewSessionPoller(SessionPollerConfig{
		CaptureLines:   30,
		StabilityCount: 2,
		Capture: func(target string, lines int) (string, error) {
			poll++
			return fmt.Sprintf(
				"> some prompt\n"+
					"assistant response line 1\n"+
					"assistant response line 2\n"+
					"• Working (%ds • esc to interrupt)\n",
				poll*3,
			), nil
		},
		OnStable: func(ctx context.Context, session, content string) error {
			mu.Lock()
			fired = append(fired, session)
			mu.Unlock()
			return nil
		},
	})

	p.Enroll("sess1", "codex", "sess1:0.0")

	p.PollOnce(ctx) // baseline
	p.PollOnce(ctx) // should fire if volatile strip works
	if len(fired) != 1 {
		t.Fatalf("expected fire with volatile lines stripped, got %v (poll count %d)", fired, poll)
	}
}

// TestPoller_VolatileStripRespectsRuntime verifies the stripper doesn't
// apply codex patterns to non-codex runtimes (otherwise a Claude pane
// with a line matching "Working (Ns)" by coincidence would be filtered
// incorrectly).
func TestPoller_VolatileStripRespectsRuntime(t *testing.T) {
	// Unknown runtime: NO stripping, so changing content keeps the
	// hash unstable.
	ctx := context.Background()
	var fired []string
	var mu sync.Mutex

	poll := 0
	p := NewSessionPoller(SessionPollerConfig{
		CaptureLines:   30,
		StabilityCount: 2,
		Capture: func(target string, lines int) (string, error) {
			poll++
			return fmt.Sprintf("• Working (%ds)\n", poll), nil
		},
		OnStable: func(ctx context.Context, session, content string) error {
			mu.Lock()
			fired = append(fired, session)
			mu.Unlock()
			return nil
		},
	})

	p.Enroll("sess1", "unknown_runtime", "sess1:0.0")

	for i := 0; i < 5; i++ {
		p.PollOnce(ctx)
	}
	if len(fired) != 0 {
		t.Fatalf("unknown runtime should NOT strip volatile lines; content changes should prevent fire, got %v", fired)
	}
}

// TestPoller_EnrollUnenroll verifies lifecycle operations.
func TestPoller_EnrollUnenroll(t *testing.T) {
	ctx := context.Background()
	var fired []string
	var mu sync.Mutex

	p := NewSessionPoller(SessionPollerConfig{
		CaptureLines:   30,
		StabilityCount: 2,
		Capture: func(target string, lines int) (string, error) {
			return "stable", nil
		},
		OnStable: func(ctx context.Context, session, content string) error {
			mu.Lock()
			fired = append(fired, session)
			mu.Unlock()
			return nil
		},
	})

	// Not enrolled: no fires
	p.PollOnce(ctx)
	p.PollOnce(ctx)
	if len(fired) != 0 {
		t.Fatalf("no enrollment → no fires, got %v", fired)
	}

	// Enroll + poll to threshold → fires
	p.Enroll("sess1", "codex", "sess1:0.0")
	p.PollOnce(ctx)
	p.PollOnce(ctx)
	if len(fired) != 1 {
		t.Fatalf("after enroll expected 1 fire, got %v", fired)
	}

	// Unenroll → subsequent polls don't fire
	p.Unenroll("sess1")
	for i := 0; i < 3; i++ {
		p.PollOnce(ctx)
	}
	if len(fired) != 1 {
		t.Fatalf("after unenroll should not fire, got %v", fired)
	}
}

// TestPoller_MultipleSessionsIndependent verifies each enrolled session
// maintains its own stability state.
func TestPoller_MultipleSessionsIndependent(t *testing.T) {
	ctx := context.Background()
	var fired []string
	var mu sync.Mutex

	contents := map[string]string{
		"sess1": "stable-1",
		"sess2": "unstable",
	}
	callCount := 0

	p := NewSessionPoller(SessionPollerConfig{
		CaptureLines:   30,
		StabilityCount: 2,
		Capture: func(target string, lines int) (string, error) {
			// Extract session from target "sess:0.0"
			session := target[:len(target)-4]
			if session == "sess2" {
				callCount++
				return fmt.Sprintf("changing-%d", callCount), nil
			}
			return contents[session], nil
		},
		OnStable: func(ctx context.Context, session, content string) error {
			mu.Lock()
			fired = append(fired, session)
			mu.Unlock()
			return nil
		},
	})

	p.Enroll("sess1", "codex", "sess1:0.0")
	p.Enroll("sess2", "codex", "sess2:0.0")

	p.PollOnce(ctx)
	p.PollOnce(ctx)
	p.PollOnce(ctx)

	// sess1 should fire exactly once; sess2 should never fire (content
	// keeps changing).
	sess1Count := 0
	sess2Count := 0
	for _, f := range fired {
		switch f {
		case "sess1":
			sess1Count++
		case "sess2":
			sess2Count++
		}
	}
	if sess1Count != 1 {
		t.Errorf("sess1 expected 1 fire, got %d (fired=%v)", sess1Count, fired)
	}
	if sess2Count != 0 {
		t.Errorf("sess2 expected 0 fires, got %d (fired=%v)", sess2Count, fired)
	}
}

// TestPoller_CaptureErrorContinuesGracefully verifies a capture error
// for one session doesn't crash the poller or affect other sessions.
func TestPoller_CaptureErrorContinuesGracefully(t *testing.T) {
	ctx := context.Background()
	var fired []string
	var mu sync.Mutex

	p := NewSessionPoller(SessionPollerConfig{
		CaptureLines:   30,
		StabilityCount: 2,
		Capture: func(target string, lines int) (string, error) {
			if target == "broken:0.0" {
				return "", errors.New("tmux capture failed")
			}
			return "stable", nil
		},
		OnStable: func(ctx context.Context, session, content string) error {
			mu.Lock()
			fired = append(fired, session)
			mu.Unlock()
			return nil
		},
	})

	p.Enroll("broken", "codex", "broken:0.0")
	p.Enroll("good", "codex", "good:0.0")

	p.PollOnce(ctx)
	p.PollOnce(ctx)

	if len(fired) != 1 || fired[0] != "good" {
		t.Fatalf("expected only 'good' to fire, got %v", fired)
	}
}

// TestStripVolatileLines_Codex ensures the runtime-specific filter
// removes codex's "Working (Ns)" timer line.
func TestStripVolatileLines_Codex(t *testing.T) {
	input := `> prompt text
assistant response
• Working (42s • esc to interrupt)
› Implement {feature}`

	stripped := stripVolatileLines("codex", input)
	if containsLine(stripped, "Working (42s") {
		t.Errorf("expected Working line stripped, got:\n%s", stripped)
	}
	if !containsLine(stripped, "> prompt text") {
		t.Errorf("expected prompt line kept, got:\n%s", stripped)
	}
	if !containsLine(stripped, "assistant response") {
		t.Errorf("expected assistant line kept, got:\n%s", stripped)
	}
}

// TestStripVolatileLines_UnknownRuntime returns input unchanged.
func TestStripVolatileLines_UnknownRuntime(t *testing.T) {
	input := "• Working (5s)\nother line"
	got := stripVolatileLines("made_up_runtime", input)
	if got != input {
		t.Errorf("unknown runtime should be pass-through\nwant: %q\ngot:  %q", input, got)
	}
}

// containsLine returns true if content contains line as a whole-line match.
func containsLine(content, line string) bool {
	for _, l := range splitLines(content) {
		if l == line {
			return true
		}
	}
	// Also allow partial matches for flexibility in tests (the "Working"
	// line's exact text includes the timer, so test asserts on a prefix).
	for _, l := range splitLines(content) {
		if len(line) <= len(l) && l[:len(line)] == line {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
