package sweep

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

// stubRegistry returns a fixed agent list. Models the daemon agent
// registry without spinning up state.State.
type stubRegistry struct {
	agents []AgentInfo
	err    error
}

func (s stubRegistry) LiveAgents(_ context.Context) ([]AgentInfo, error) {
	return s.agents, s.err
}

// alwaysAlive marks every session as existing. selectiveAlive uses a
// set, allowing tests to model "some live, some dead" mixes.
func alwaysAlive() SessionExistsFn { return func(string) bool { return true } }
func selectiveAlive(alive map[string]bool) SessionExistsFn {
	return func(session string) bool { return alive[session] }
}

// staticActivity returns a fixed string for every session.
func staticActivity(s string) WindowActivityFn {
	return func(context.Context, string) (string, error) { return s, nil }
}

// perSessionActivity returns different strings per session; useful for
// asserting LastActivity values across multiple agents in one pass.
func perSessionActivity(byName map[string]string) WindowActivityFn {
	return func(_ context.Context, session string) (string, error) {
		v, ok := byName[session]
		if !ok {
			return "", errors.New("no activity for " + session)
		}
		return v, nil
	}
}

func TestPaneSource_RegistryError_PropagatesError(t *testing.T) {
	src := NewDaemonPaneSourceWithDeps(
		stubRegistry{err: errors.New("registry down")},
		alwaysAlive(),
		staticActivity("1700000000"),
	)
	_, err := src.LivePanes(ctx)
	if err == nil {
		t.Error("expected registry error to propagate")
	}
}

func TestPaneSource_EmptyRegistry_ReturnsEmptySlice(t *testing.T) {
	src := NewDaemonPaneSourceWithDeps(
		stubRegistry{},
		alwaysAlive(),
		staticActivity("1700000000"),
	)
	panes, err := src.LivePanes(ctx)
	if err != nil {
		t.Fatalf("LivePanes: %v", err)
	}
	if len(panes) != 0 {
		t.Errorf("empty registry should yield zero panes; got %d", len(panes))
	}
}

func TestPaneSource_AgentWithoutTmuxSession_Skipped(t *testing.T) {
	src := NewDaemonPaneSourceWithDeps(
		stubRegistry{agents: []AgentInfo{
			{Name: "docs_bot", TmuxSession: ""}, // headless / remote
			{Name: "billing_bot", TmuxSession: "billing"},
		}},
		alwaysAlive(),
		staticActivity("1700000000"),
	)
	panes, err := src.LivePanes(ctx)
	if err != nil {
		t.Fatalf("LivePanes: %v", err)
	}
	if len(panes) != 1 || panes[0].AgentName != "billing_bot" {
		t.Errorf("expected only billing_bot; got %+v", panes)
	}
}

func TestPaneSource_FiltersDeadSessions(t *testing.T) {
	src := NewDaemonPaneSourceWithDeps(
		stubRegistry{agents: []AgentInfo{
			{Name: "live_agent", TmuxSession: "live"},
			{Name: "dead_agent", TmuxSession: "dead"},
		}},
		selectiveAlive(map[string]bool{"live": true}),
		staticActivity("1700000000"),
	)
	panes, err := src.LivePanes(ctx)
	if err != nil {
		t.Fatalf("LivePanes: %v", err)
	}
	if len(panes) != 1 || panes[0].AgentName != "live_agent" {
		t.Errorf("dead session should be filtered; got %+v", panes)
	}
}

func TestPaneSource_TmuxActivityError_AgentSkipped(t *testing.T) {
	src := NewDaemonPaneSourceWithDeps(
		stubRegistry{agents: []AgentInfo{
			{Name: "good_agent", TmuxSession: "good"},
			{Name: "bad_agent", TmuxSession: "bad"},
		}},
		alwaysAlive(),
		perSessionActivity(map[string]string{
			"good": "1700000000",
			// "bad" missing → activity fn returns error
		}),
	)
	panes, err := src.LivePanes(ctx)
	if err != nil {
		t.Fatalf("LivePanes: %v", err)
	}
	// good_agent should still appear; the bad_agent error should be
	// absorbed (transient tmux teardown race).
	if len(panes) != 1 || panes[0].AgentName != "good_agent" {
		t.Errorf("transient tmux error should skip just the failing agent; got %+v", panes)
	}
}

func TestPaneSource_EmptyWindowActivity_UsesNow(t *testing.T) {
	src := NewDaemonPaneSourceWithDeps(
		stubRegistry{agents: []AgentInfo{
			{Name: "fresh_agent", TmuxSession: "fresh"},
		}},
		alwaysAlive(),
		staticActivity(""), // freshly-created session, no activity yet
	)
	panes, err := src.LivePanes(ctx)
	if err != nil {
		t.Fatalf("LivePanes: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane; got %d", len(panes))
	}
	// LastActivity should be ~now (within a small window); compare
	// liberally so test isn't flaky on slow CI.
	delta := time.Since(panes[0].LastActivity).Abs()
	if delta > 5*time.Second {
		t.Errorf("LastActivity = %v (delta from now = %v); want ~now to avoid false-positive sweep on fresh sessions",
			panes[0].LastActivity, delta)
	}
}

func TestPaneSource_ParsesIntegerTimestamp(t *testing.T) {
	stamp := int64(1700000000)
	src := NewDaemonPaneSourceWithDeps(
		stubRegistry{agents: []AgentInfo{
			{Name: "docs_bot", TmuxSession: "docs"},
		}},
		alwaysAlive(),
		staticActivity(strconv.FormatInt(stamp, 10)+"\n"), // trailing newline tmux-style
	)
	panes, err := src.LivePanes(ctx)
	if err != nil {
		t.Fatalf("LivePanes: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane; got %d", len(panes))
	}
	if panes[0].LastActivity.Unix() != stamp {
		t.Errorf("LastActivity = %d, want %d", panes[0].LastActivity.Unix(), stamp)
	}
}

func TestPaneSource_TmuxTargetDefaultsToWindowZeroPaneZero(t *testing.T) {
	src := NewDaemonPaneSourceWithDeps(
		stubRegistry{agents: []AgentInfo{
			{Name: "docs_bot", TmuxSession: "docs"},
		}},
		alwaysAlive(),
		staticActivity("1700000000"),
	)
	panes, _ := src.LivePanes(ctx)
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane; got %d", len(panes))
	}
	if panes[0].TmuxTarget != "docs:0.0" {
		t.Errorf("TmuxTarget = %q, want docs:0.0", panes[0].TmuxTarget)
	}
}

func TestParseWindowActivity_Boundaries(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		in   string
		want func(time.Time) bool // predicate on result
	}{
		{"1700000000", func(got time.Time) bool { return got.Unix() == 1700000000 }},
		{"1700000000\n", func(got time.Time) bool { return got.Unix() == 1700000000 }},
		{"  1700000000  ", func(got time.Time) bool { return got.Unix() == 1700000000 }},
		{"", func(got time.Time) bool { return got.Sub(now).Abs() < 5*time.Second }},
		{"not-a-number", func(got time.Time) bool { return got.Sub(now).Abs() < 5*time.Second }},
	}
	for _, c := range cases {
		got := parseWindowActivity(c.in)
		if !c.want(got) {
			t.Errorf("parseWindowActivity(%q) = %v (failed predicate)", c.in, got)
		}
	}
}

func TestPaneSource_ProductionConstructorWired(t *testing.T) {
	src := NewDaemonPaneSource(stubRegistry{})
	if src == nil {
		t.Fatal("NewDaemonPaneSource returned nil")
	}
	if src.sessionExists == nil || src.windowActivity == nil {
		t.Error("production constructor should default sessionExists + windowActivity")
	}
}
