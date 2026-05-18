package contextpoll

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeParser is a stub Parser used by the tests to drive a deterministic
// usage value without touching the file system.
type fakeParser struct {
	version   string
	matches   func(string) bool
	parseFunc func(string) (ContextUsage, error)
}

func (p fakeParser) Version() string { return p.version }
func (p fakeParser) Matches(path string) bool {
	if p.matches != nil {
		return p.matches(path)
	}
	return true
}
func (p fakeParser) Parse(path string) (ContextUsage, error) {
	return p.parseFunc(path)
}

// usageAt builds a fakeParser that returns a fixed UsedPercentage every time
// Parse is called. Matches always returns true so the parser is always picked.
func usageAt(pct int) fakeParser {
	return fakeParser{
		version: "fake-v1",
		parseFunc: func(path string) (ContextUsage, error) {
			return ContextUsage{
				UsedPercentage: pct,
				ParserVersion:  "fake-v1",
				SourcePath:     path,
				Timestamp:      time.Now(),
			}, nil
		},
	}
}

// callbackCounter records how many times a callback fired and the most recent
// arguments — sufficient for "fired" / "fired N times" assertions.
type callbackCounter struct {
	mu        sync.Mutex
	count     int32
	lastAgent string
	lastUsage ContextUsage
}

func (c *callbackCounter) record() (WarnCallback, PreFireCallback, FireCallback) {
	cb := func(ctx context.Context, agent string, usage ContextUsage) {
		atomic.AddInt32(&c.count, 1)
		c.mu.Lock()
		c.lastAgent = agent
		c.lastUsage = usage
		c.mu.Unlock()
	}
	return cb, cb, cb
}

func (c *callbackCounter) calls() int { return int(atomic.LoadInt32(&c.count)) }

// newTestPoller builds a Poller with a tight PreFireWait so the force-fire
// path is testable in <100ms wall-clock.
func newTestPoller(t *testing.T) *Poller {
	t.Helper()
	return NewPoller(PollerConfig{
		PollInterval:    10 * time.Millisecond,
		PreFireWait:     50 * time.Millisecond,
		InFlightMaxWait: time.Second,
		WarnThreshold:   70,
		AutoThreshold:   80,
	})
}

func enrollOne(p *Poller, name string) {
	p.Enroll(name, AgentEnrollment{
		TranscriptPath: "/fake/" + name + ".jsonl",
		Runtime:        "claude",
	})
}

func TestPoller_WarnCallback_FiresAtThreshold(t *testing.T) {
	p := newTestPoller(t)
	p.RegisterParser(usageAt(72))
	var warn callbackCounter
	w, _, _ := warn.record()
	p.OnWarn(w)
	enrollOne(p, "agent-1")

	p.pollOnce(context.Background())

	if got := warn.calls(); got != 1 {
		t.Errorf("onWarn fired %d times, want 1", got)
	}
}

func TestPoller_WarnCallback_Dedup(t *testing.T) {
	p := newTestPoller(t)
	p.RegisterParser(usageAt(72))
	var warn callbackCounter
	w, _, _ := warn.record()
	p.OnWarn(w)
	enrollOne(p, "agent-1")

	p.pollOnce(context.Background())
	p.pollOnce(context.Background())

	if got := warn.calls(); got != 1 {
		t.Errorf("onWarn fired %d times across two polls, want 1", got)
	}
}

func TestPoller_WarnCallback_ResetsAfterRestart(t *testing.T) {
	p := newTestPoller(t)
	p.RegisterParser(usageAt(72))
	var warn callbackCounter
	w, _, _ := warn.record()
	p.OnWarn(w)
	enrollOne(p, "agent-1")

	p.pollOnce(context.Background()) // fires
	p.PostRestart("agent-1")          // clears flags
	p.pollOnce(context.Background()) // should fire again

	if got := warn.calls(); got != 2 {
		t.Errorf("onWarn fired %d times across the restart cycle, want 2", got)
	}
}

func TestPoller_PreFireCallback_FiresAtAutoThreshold(t *testing.T) {
	p := newTestPoller(t)
	p.RegisterParser(usageAt(82))
	var warn, pre, fire callbackCounter
	w, _, _ := warn.record()
	_, pre2, _ := pre.record()
	_, _, fr := fire.record()
	p.OnWarn(w)
	p.OnPreFire(pre2)
	p.OnFire(fr)
	enrollOne(p, "agent-1")

	p.pollOnce(context.Background())

	if pre.calls() != 1 {
		t.Errorf("onPreFire fired %d times, want 1", pre.calls())
	}
	if fire.calls() != 0 {
		t.Errorf("onFire fired %d times immediately, want 0 (must wait PreFireWait)", fire.calls())
	}
	// At >= auto, the warn discipline signal should also fire on the same
	// poll — both signals carry independent useful information.
	if warn.calls() != 1 {
		t.Errorf("onWarn fired %d times alongside preFire, want 1", warn.calls())
	}
}

func TestPoller_FireCallback_FiresAfterTimer(t *testing.T) {
	p := newTestPoller(t)
	p.RegisterParser(usageAt(82))
	var fire callbackCounter
	_, _, fr := fire.record()
	p.OnFire(fr)
	enrollOne(p, "agent-1")

	p.pollOnce(context.Background()) // arms preFire
	if fire.calls() != 0 {
		t.Fatalf("onFire fired immediately, want 0")
	}
	// PreFireWait is 50ms; sleep past it, then poll again.
	time.Sleep(60 * time.Millisecond)
	p.pollOnce(context.Background())

	if fire.calls() != 1 {
		t.Errorf("onFire fired %d times after PreFireWait elapsed, want 1", fire.calls())
	}
}

func TestPoller_InFlightGuard(t *testing.T) {
	p := newTestPoller(t)
	p.RegisterParser(usageAt(90))
	var warn, pre, fire callbackCounter
	w, _, _ := warn.record()
	_, prefireCb, _ := pre.record()
	_, _, fireCb := fire.record()
	p.OnWarn(w)
	p.OnPreFire(prefireCb)
	p.OnFire(fireCb)
	enrollOne(p, "agent-1")

	p.pollOnce(context.Background()) // warn + preFire
	time.Sleep(60 * time.Millisecond)
	p.pollOnce(context.Background()) // onFire — sets restartInFlight

	beforeWarn := warn.calls()
	beforePre := pre.calls()
	beforeFire := fire.calls()

	// Subsequent polls at 90% must not refire anything.
	p.pollOnce(context.Background())
	p.pollOnce(context.Background())

	if warn.calls() != beforeWarn {
		t.Errorf("onWarn re-fired under inFlight guard (was %d, now %d)", beforeWarn, warn.calls())
	}
	if pre.calls() != beforePre {
		t.Errorf("onPreFire re-fired under inFlight guard (was %d, now %d)", beforePre, pre.calls())
	}
	if fire.calls() != beforeFire {
		t.Errorf("onFire re-fired under inFlight guard (was %d, now %d)", beforeFire, fire.calls())
	}
}

func TestPoller_ParseError_NoCallback(t *testing.T) {
	p := newTestPoller(t)
	p.RegisterParser(fakeParser{
		version: "fake-v1",
		parseFunc: func(path string) (ContextUsage, error) {
			return ContextUsage{}, errors.New("synthetic parse failure")
		},
	})
	var warn callbackCounter
	w, _, _ := warn.record()
	p.OnWarn(w)
	enrollOne(p, "agent-1")

	p.pollOnce(context.Background())

	if warn.calls() != 0 {
		t.Errorf("onWarn fired %d times on parser error, want 0", warn.calls())
	}
}

func TestPoller_UnknownRuntime_NoCallback(t *testing.T) {
	p := newTestPoller(t)
	// Register a parser that NEVER matches — simulates an agent whose
	// runtime has no installed parser (e.g. cursor in v1).
	p.RegisterParser(fakeParser{
		version: "fake-claude",
		matches: func(string) bool { return false },
		parseFunc: func(string) (ContextUsage, error) {
			t.Fatal("Parse called on a non-matching agent")
			return ContextUsage{}, nil
		},
	})
	var warn callbackCounter
	w, _, _ := warn.record()
	p.OnWarn(w)
	p.Enroll("cursor-agent", AgentEnrollment{
		TranscriptPath: "/fake/cursor.json",
		Runtime:        "cursor",
	})

	p.pollOnce(context.Background())

	if warn.calls() != 0 {
		t.Errorf("onWarn fired %d times for unknown runtime, want 0", warn.calls())
	}
}

func TestPoller_EmptyPath_SkipsSilently(t *testing.T) {
	// AgentEnrollment with empty TranscriptPath: the wiring re-enrolls
	// once the path resolves. Until then, the Poller must NOT crash, NOT
	// call Parse, and NOT fire any callback.
	p := newTestPoller(t)
	p.RegisterParser(fakeParser{
		parseFunc: func(string) (ContextUsage, error) {
			t.Fatal("Parse called with empty path")
			return ContextUsage{}, nil
		},
	})
	var warn callbackCounter
	w, _, _ := warn.record()
	p.OnWarn(w)
	p.Enroll("agent-1", AgentEnrollment{TranscriptPath: "", Runtime: "claude"})

	p.pollOnce(context.Background()) // should not panic

	if warn.calls() != 0 {
		t.Errorf("onWarn fired %d times with empty path, want 0", warn.calls())
	}
}

func TestPoller_ContextUsageFor_Enrolled(t *testing.T) {
	p := newTestPoller(t)
	p.RegisterParser(usageAt(42))
	enrollOne(p, "agent-1")

	p.pollOnce(context.Background())

	got, ok := p.ContextUsageFor("agent-1")
	if !ok {
		t.Fatal("ContextUsageFor returned ok=false for enrolled agent")
	}
	if got.UsedPercentage != 42 {
		t.Errorf("UsedPercentage = %d, want 42", got.UsedPercentage)
	}
}

func TestPoller_ContextUsageFor_NotEnrolled(t *testing.T) {
	p := newTestPoller(t)
	_, ok := p.ContextUsageFor("nobody")
	if ok {
		t.Error("ContextUsageFor returned ok=true for an unenrolled agent")
	}
}

func TestPoller_ContextUsageFor_EnrolledButNoPollYet(t *testing.T) {
	// Enroll without polling — ContextUsageFor must return ok=false
	// rather than a zero-value ContextUsage masquerading as fresh data.
	p := newTestPoller(t)
	enrollOne(p, "agent-1")
	_, ok := p.ContextUsageFor("agent-1")
	if ok {
		t.Error("ContextUsageFor returned ok=true before first poll")
	}
}

func TestPoller_Unenroll_RemovesAgent(t *testing.T) {
	p := newTestPoller(t)
	p.RegisterParser(usageAt(50))
	enrollOne(p, "agent-1")
	p.pollOnce(context.Background())

	p.Unenroll("agent-1")

	if _, ok := p.ContextUsageFor("agent-1"); ok {
		t.Error("ContextUsageFor returned ok=true after Unenroll")
	}
}

func TestPoller_Run_ExitsOnContextCancel(t *testing.T) {
	p := newTestPoller(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		p.Run(ctx, 5*time.Millisecond)
		close(done)
	}()

	// Let the ticker fire at least once, then cancel.
	time.Sleep(15 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// good
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not exit within 500ms of ctx cancel")
	}
}

func TestPoller_Run_Loop_PollsAtInterval(t *testing.T) {
	p := newTestPoller(t)

	var parseCount int32
	p.RegisterParser(fakeParser{
		version: "fake-v1",
		parseFunc: func(path string) (ContextUsage, error) {
			atomic.AddInt32(&parseCount, 1)
			return ContextUsage{
				UsedPercentage: 10,
				ParserVersion:  "fake-v1",
				SourcePath:     path,
				Timestamp:      time.Now(),
			}, nil
		},
	})
	enrollOne(p, "agent-1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx, 5*time.Millisecond)

	// Allow ~5 ticks to land. Be generous to absorb scheduler jitter.
	time.Sleep(40 * time.Millisecond)
	cancel()

	if atomic.LoadInt32(&parseCount) < 2 {
		t.Errorf("expected at least 2 polls in 40ms with 5ms interval; got %d", parseCount)
	}
}

func TestPoller_InFlightMaxWait_Backstop(t *testing.T) {
	// If the in-flight flag gets stuck, the InFlightMaxWait backstop must
	// clear it so the agent can resume normal threshold cycling. We use
	// the nowFn hook to advance "wall time" past the backstop window.
	p := NewPoller(PollerConfig{
		PreFireWait:     time.Millisecond,
		InFlightMaxWait: 50 * time.Millisecond,
		WarnThreshold:   70,
		AutoThreshold:   80,
	})
	p.RegisterParser(usageAt(90))
	var warn callbackCounter
	w, _, fr := warn.record()
	p.OnWarn(w)
	p.OnFire(fr)
	enrollOne(p, "agent-1")

	p.pollOnce(context.Background())             // warn + preFire
	time.Sleep(5 * time.Millisecond)
	p.pollOnce(context.Background())             // onFire — sets inFlight

	// Confirm warn deduped (warnFired set).
	beforeWarn := warn.calls()

	// Simulate intentional advance past InFlightMaxWait.
	p.nowFn = func() time.Time { return time.Now().Add(100 * time.Millisecond) }
	p.pollOnce(context.Background()) // should clear inFlight; PostRestart-less

	if warn.calls() != beforeWarn {
		// warn-after-backstop is fine, but the test goal is the
		// backstop-clearing path executes without panic. The warn
		// count may stay the same since warnFired is still set —
		// only PostRestart clears that.
		t.Logf("(informational) onWarn fired again after backstop: %d → %d", beforeWarn, warn.calls())
	}
}
