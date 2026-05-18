package rpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// --- Branch 1: live pane ---

func TestFallbackChain_LivePane_Running_UsesPaneSnippet(t *testing.T) {
	captured := "line 1\nline 2\nline 3"
	capture := func(_ context.Context, name string, n int) (string, error) {
		if name != "docs_bot" {
			t.Errorf("capture target = %q; want docs_bot", name)
		}
		if n != 10 {
			t.Errorf("capture lines = %d; want 10", n)
		}
		return captured, nil
	}
	state := AgentRenderState{
		JobCurrentState: "running",
		ActiveJobID:     "docs_bot_wake",
		Elapsed:         42 * time.Second,
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", "", state, capture, nil)

	if !strings.Contains(got, "line 1") {
		t.Errorf("output missing pane snippet; got %q", got)
	}
	if !strings.Contains(got, "docs_bot_wake") {
		t.Errorf("output missing ActiveJobID; got %q", got)
	}
	if !strings.Contains(got, "42s") {
		t.Errorf("output missing elapsed; got %q", got)
	}
}

func TestFallbackChain_NotRunning_SkipsLivePane(t *testing.T) {
	capture := func(_ context.Context, _ string, _ int) (string, error) {
		t.Error("capture should NOT be called when state != running")
		return "should not fire", nil
	}
	state := AgentRenderState{
		JobCurrentState: "scheduled",
		LastCompletedAt: time.Now(),
		LastRunState:    "completed",
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", "", state, capture, nil)

	if strings.Contains(got, "should not fire") {
		t.Errorf("branch 1 fired when state != running; got %q", got)
	}
}

func TestFallbackChain_LivePane_CaptureError_FallsThrough(t *testing.T) {
	capture := func(_ context.Context, _ string, _ int) (string, error) {
		return "", errors.New("tmux unavailable")
	}
	outbound := func(_ context.Context, _ string) (*OutboundMessage, error) {
		return &OutboundMessage{MessageID: "msg-3", Subject: "fallback"}, nil
	}
	state := AgentRenderState{JobCurrentState: "running"}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", "", state, capture, outbound)

	if !strings.Contains(got, "Last said: msg-3") {
		t.Errorf("branch 1 capture-error should fall through to branch 3; got %q", got)
	}
}

// TestFallbackChain_LivePane_CaptureEmptyString_FallsThrough pins
// the non-error empty-content path: capture returns ("", nil) — no
// underlying error but the pane has nothing to render. Branch 1
// must fall through to subsequent branches rather than rendering an
// empty pane block. Without this, the expanded view would show
// "<blank>\n\n[active job: ..., elapsed: ...]" — misleading
// "agent is running but produced no output" framing when the
// capture itself returned cleanly.
func TestFallbackChain_LivePane_CaptureEmptyString_FallsThrough(t *testing.T) {
	capture := func(_ context.Context, _ string, _ int) (string, error) {
		return "", nil // clean call, but pane is genuinely empty
	}
	outbound := func(_ context.Context, _ string) (*OutboundMessage, error) {
		return &OutboundMessage{MessageID: "msg-empty", Subject: "fallback-on-empty"}, nil
	}
	state := AgentRenderState{
		JobCurrentState: "running",
		ActiveJobID:     "docs_bot_wake",
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", "", state, capture, outbound)

	if strings.Contains(got, "[active job:") {
		t.Errorf("empty-pane should NOT render the active-job suffix (misleading framing); got %q", got)
	}
	if !strings.Contains(got, "Last said: msg-empty") {
		t.Errorf("empty-pane should fall through to branch 3; got %q", got)
	}
}

// --- Branch 2: summary.md ---

func TestFallbackChain_SummaryMd_NewerThanLastCompleted_Used(t *testing.T) {
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, "docs_bot")
	if err := os.MkdirAll(agentDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summaryPath := filepath.Join(agentDir, "summary.md")
	content := []byte("Fresh summary content.")
	if err := os.WriteFile(summaryPath, content, 0o600); err != nil {
		t.Fatalf("write summary.md: %v", err)
	}
	// Make sure mtime is unambiguously after lastCompletedAt.
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(summaryPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	state := AgentRenderState{
		JobCurrentState: "scheduled",
		LastCompletedAt: time.Now().Add(-1 * time.Hour), // older than file mtime
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", agentsDir, state, nil, nil)

	if got != "Fresh summary content." {
		t.Errorf("expected summary.md content verbatim; got %q", got)
	}
}

// TestFallbackChain_SummaryMd_OlderThanLastCompleted_Skipped pins the
// IMPORTANT #3 boundary check: a summary.md left over from a prior
// job run must NOT silently surface as the current view. Without
// this gate operators see stale data attributed to the current run.
func TestFallbackChain_SummaryMd_OlderThanLastCompleted_Skipped(t *testing.T) {
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, "docs_bot")
	if err := os.MkdirAll(agentDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summaryPath := filepath.Join(agentDir, "summary.md")
	if err := os.WriteFile(summaryPath, []byte("Stale summary."), 0o600); err != nil {
		t.Fatalf("write summary.md: %v", err)
	}
	// Mark summary mtime as older than the last-completed time.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(summaryPath, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	state := AgentRenderState{
		JobCurrentState: "scheduled",
		LastCompletedAt: time.Now().Add(-1 * time.Hour), // newer than file
		LastRunState:    "completed",
	}
	outbound := func(_ context.Context, _ string) (*OutboundMessage, error) {
		return &OutboundMessage{MessageID: "msg-7", Subject: "newer message"}, nil
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", agentsDir, state, nil, outbound)

	if strings.Contains(got, "Stale summary") {
		t.Errorf("stale summary leaked into output: %q", got)
	}
	if !strings.Contains(got, "Last said: msg-7") {
		t.Errorf("expected fall-through to branch 3 (outbound); got %q", got)
	}
}

// TestFallbackChain_SummaryMd_ExactlyAtBoundary documents Go's
// time.After being a strict greater-than: when mtime ==
// LastCompletedAt, branch 2 is SKIPPED. The fall-through to branch
// 3 confirms this. Future implementers reading the renderer
// shouldn't "fix" this boundary into an inclusive >= comparison —
// the strict-greater semantics protect against an mtime that
// accidentally matches the completion timestamp from leaking
// stale content.
func TestFallbackChain_SummaryMd_ExactlyAtBoundary_BoundaryBehavior(t *testing.T) {
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, "docs_bot")
	if err := os.MkdirAll(agentDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summaryPath := filepath.Join(agentDir, "summary.md")
	if err := os.WriteFile(summaryPath, []byte("Boundary summary."), 0o600); err != nil {
		t.Fatalf("write summary.md: %v", err)
	}
	info, err := os.Stat(summaryPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Pin LastCompletedAt = exactly the file mtime.
	state := AgentRenderState{
		JobCurrentState: "scheduled",
		LastCompletedAt: info.ModTime(),
		LastRunState:    "completed",
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", agentsDir, state, nil, nil)

	if strings.Contains(got, "Boundary summary") {
		t.Errorf("equal-time mtime leaked into output (Go time.After is strict '>'): %q", got)
	}
}

// --- Branch 3: outbound message ---

func TestFallbackChain_OutboundDispatched_Rendered(t *testing.T) {
	outbound := func(_ context.Context, name string) (*OutboundMessage, error) {
		if name != "docs_bot" {
			t.Errorf("outbound name = %q; want docs_bot", name)
		}
		return &OutboundMessage{MessageID: "msg-42", Subject: "Daily report"}, nil
	}
	state := AgentRenderState{
		JobCurrentState: "scheduled",
		LastCompletedAt: time.Now().Add(-1 * time.Hour),
		LastRunState:    "completed",
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", "", state, nil, outbound)

	want := "Last said: msg-42 · Daily report"
	if got != want {
		t.Errorf("output = %q; want %q", got, want)
	}
}

func TestFallbackChain_OutboundNilMessage_FallsThroughToBranch4(t *testing.T) {
	outbound := func(_ context.Context, _ string) (*OutboundMessage, error) {
		return nil, nil // agent has never sent a message
	}
	completed := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	state := AgentRenderState{
		JobCurrentState: "scheduled",
		LastCompletedAt: completed,
		LastRunState:    "completed",
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", "", state, nil, outbound)

	if !strings.Contains(got, "Last run: completed") {
		t.Errorf("expected branch 4 fallback; got %q", got)
	}
	if !strings.Contains(got, "2026-05-18T12:00:00Z") {
		t.Errorf("branch 4 missing RFC3339 timestamp; got %q", got)
	}
}

// --- Branch 4: "No summary" ---

func TestFallbackChain_NoData_StaticNoSummary(t *testing.T) {
	// Every dep returns no data. Branch 4 must render.
	state := AgentRenderState{} // zero values; LastCompletedAt is zero
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", "", state, nil, nil)

	if got != "No summary." {
		t.Errorf("output = %q; want exact 'No summary.'", got)
	}
}

func TestFallbackChain_NoData_WithLastRunState(t *testing.T) {
	state := AgentRenderState{
		LastCompletedAt: time.Date(2026, 5, 18, 9, 30, 0, 0, time.UTC),
		LastRunState:    "failed",
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", "", state, nil, nil)

	if !strings.Contains(got, "Last run: failed") {
		t.Errorf("output missing last_run_state; got %q", got)
	}
	if !strings.Contains(got, "2026-05-18T09:30:00Z") {
		t.Errorf("output missing timestamp; got %q", got)
	}
}

// --- Cross-branch precedence ---

// --- Task 59: RenderJournal ---

// fakeLifecycleStore minimally satisfies state.AgentLifecycleStore
// for the RenderJournal happy-path / error / empty cases. Only
// ListByAgent gets non-trivial fixtures; the other methods are
// satisfied by no-ops so the type compiles.
type fakeLifecycleStore struct {
	events    []lcEvent
	err       error
	calls     int
	lastLimit int
}

type lcEvent struct {
	When   time.Time
	Kind   string
	Reason string
}

func (f *fakeLifecycleStore) ListByAgent(_ context.Context, _ string, limit int) ([]state.AgentLifecycleEvent, error) {
	f.calls++
	f.lastLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	out := make([]state.AgentLifecycleEvent, 0, len(f.events))
	for _, e := range f.events {
		out = append(out, state.AgentLifecycleEvent{
			AgentName: "docs_bot",
			EventTime: e.When,
			EventKind: state.AgentLifecycleEventKind(e.Kind),
			Reason:    e.Reason,
		})
	}
	return out, nil
}
func (f *fakeLifecycleStore) ListByAgents(_ context.Context, _ []string, _ int) (map[string][]state.AgentLifecycleEvent, error) {
	return map[string][]state.AgentLifecycleEvent{}, nil
}
func (f *fakeLifecycleStore) Append(_ context.Context, _ state.AgentLifecycleEvent) (int64, error) {
	return 0, nil
}
func (f *fakeLifecycleStore) LoopGuardCount(_ context.Context, _ string, _ state.AgentLifecycleEventKind, _ int) (int, error) {
	return 0, nil
}
func (f *fakeLifecycleStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func TestRenderJournal_HappyPath(t *testing.T) {
	store := &fakeLifecycleStore{
		events: []lcEvent{
			{When: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), Kind: "respawn_fired", Reason: "pane gone"},
			{When: time.Date(2026, 5, 18, 11, 30, 0, 0, time.UTC), Kind: "crash_detected", Reason: "health-check"},
		},
	}
	out := RenderJournal(context.Background(), "docs_bot", store)

	if !strings.Contains(out, "respawn_fired") {
		t.Errorf("journal missing respawn_fired event; got %q", out)
	}
	if !strings.Contains(out, "crash_detected") {
		t.Errorf("journal missing crash_detected event; got %q", out)
	}
	if !strings.Contains(out, "2026-05-18T12:00:00Z") {
		t.Errorf("journal missing RFC3339 timestamp; got %q", out)
	}
	if !strings.Contains(out, "pane gone") {
		t.Errorf("journal missing reason; got %q", out)
	}
	if store.lastLimit != journalDefaultLimit {
		t.Errorf("ListByAgent limit = %d; want %d", store.lastLimit, journalDefaultLimit)
	}
}

func TestRenderJournal_EmptyEvents_StaticMessage(t *testing.T) {
	store := &fakeLifecycleStore{events: nil}
	out := RenderJournal(context.Background(), "docs_bot", store)
	if !strings.Contains(out, "No journal entries") {
		t.Errorf("expected static no-entries message; got %q", out)
	}
}

func TestRenderJournal_StoreError_InlineError(t *testing.T) {
	store := &fakeLifecycleStore{err: errors.New("db locked")}
	out := RenderJournal(context.Background(), "docs_bot", store)
	if !strings.Contains(out, "error reading journal") {
		t.Errorf("expected inline error prefix; got %q", out)
	}
	if !strings.Contains(out, "db locked") {
		t.Errorf("expected underlying error detail; got %q", out)
	}
}

func TestRenderJournal_NilStore_StaticMessage(t *testing.T) {
	out := RenderJournal(context.Background(), "docs_bot", nil)
	if !strings.Contains(out, "Journal unavailable") {
		t.Errorf("expected nil-store fallback message; got %q", out)
	}
}

func TestRenderJournal_EventWithoutReason_RendersDashPlaceholder(t *testing.T) {
	store := &fakeLifecycleStore{
		events: []lcEvent{
			{When: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), Kind: "respawn_fired", Reason: ""},
		},
	}
	out := RenderJournal(context.Background(), "docs_bot", store)
	if !strings.Contains(out, "—") {
		t.Errorf("missing dash placeholder for empty reason; got %q", out)
	}
}

// --- Task 58: FilesRPCUnavailableMessage ---

// TestFilesRPCUnavailableMessage_NonEmpty pins that the constant
// carries a non-empty operator-facing string. Trivial but catches
// an accidental empty-string regression that would silently leave
// the --files feature-detect path printing nothing.
func TestFilesRPCUnavailableMessage_NonEmpty(t *testing.T) {
	if FilesRPCUnavailableMessage == "" {
		t.Error("FilesRPCUnavailableMessage must be non-empty for the --files feature-detect path")
	}
	if !strings.Contains(FilesRPCUnavailableMessage, "files") {
		t.Errorf("message should mention 'files'; got %q", FilesRPCUnavailableMessage)
	}
	if !strings.Contains(FilesRPCUnavailableMessage, "unavailable") {
		t.Errorf("message should signal unavailability; got %q", FilesRPCUnavailableMessage)
	}
}

// TestFallbackChain_AllBranchesAvailable_LivePaneWins pins the
// recency-ranked precedence: when every fallback source has data,
// the running pane (branch 1) is the freshest signal and wins.
func TestFallbackChain_AllBranchesAvailable_LivePaneWins(t *testing.T) {
	agentsDir := t.TempDir()
	agentDir := filepath.Join(agentsDir, "docs_bot")
	if err := os.MkdirAll(agentDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summaryPath := filepath.Join(agentDir, "summary.md")
	if err := os.WriteFile(summaryPath, []byte("Summary content."), 0o600); err != nil {
		t.Fatalf("write summary.md: %v", err)
	}

	capture := func(_ context.Context, _ string, _ int) (string, error) {
		return "PANE LINE", nil
	}
	outbound := func(_ context.Context, _ string) (*OutboundMessage, error) {
		return &OutboundMessage{MessageID: "msg-1", Subject: "outbound"}, nil
	}
	state := AgentRenderState{
		JobCurrentState: "running",
		ActiveJobID:     "docs_bot_wake",
		LastCompletedAt: time.Now().Add(-1 * time.Hour),
		LastRunState:    "completed",
	}
	got := RenderBodyFallbackChain(context.Background(), "docs_bot", agentsDir, state, capture, outbound)

	if !strings.Contains(got, "PANE LINE") {
		t.Errorf("branch 1 (live pane) should win precedence; got %q", got)
	}
	if strings.Contains(got, "Summary content") || strings.Contains(got, "outbound") {
		t.Errorf("lower-precedence branches leaked into output: %q", got)
	}
}
