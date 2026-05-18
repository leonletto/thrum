package agentstate_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/agentstate"
)

// fixtureBasic returns a minimal-but-complete state.md content
// that exercises every section the parser knows about. Used as
// the baseline for round-trip and structural tests.
func fixtureBasic() string {
	return "# Agent State — alpha\n" +
		"\n" +
		"> **Last updated:** 2026-05-18T15:32:18Z **Last run:** docs-bot-g3 · success\n" +
		"\n" +
		"## Session history\n" +
		"\n" +
		"### Verbatim (most recent first)\n" +
		"\n" +
		"1. ses_004 · 2026-05-18 — Fourth session: closed E6.2.\n" +
		"2. ses_003 · 2026-05-17 — Third session: drafted state.md format.\n" +
		"3. ses_002 · 2026-05-16 — Second session: sketched parser.\n" +
		"4. ses_001 · 2026-05-15 — First session: spec read.\n" +
		"\n" +
		"### Summary blocks (most recent first)\n" +
		"\n" +
		"**Block A** (sessions ses_000a–ses_000e): Earlier work on B-B1 E6.1.\n" +
		"\n" +
		"## Last worked on\n" +
		"\n" +
		"I last did the state.md parser. Open thread: PromoteAndDrop yo-yo test.\n" +
		"\n" +
		"## Planning next\n" +
		"\n" +
		"Next wake should look at update-agent-state skill because it consumes PromoteAndDrop.\n" +
		"\n" +
		"## Reference table\n" +
		"\n" +
		"- `internal/agentstate/format.go`: State.md parser + writer + PromoteAndDrop.\n" +
		"- `dev-docs/specs/2026-05-15-thrum-agents-b-b1-design.md`: Spec §7.5 sliding window.\n"
}

func TestParse_HappyPath(t *testing.T) {
	s, err := agentstate.Parse(fixtureBasic())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.AgentName != "alpha" {
		t.Errorf("AgentName: got %q, want 'alpha'", s.AgentName)
	}
	expectedTime := time.Date(2026, 5, 18, 15, 32, 18, 0, time.UTC)
	if !s.LastUpdated.Equal(expectedTime) {
		t.Errorf("LastUpdated: got %v, want %v", s.LastUpdated, expectedTime)
	}
	if s.LastRunID != "docs-bot-g3" {
		t.Errorf("LastRunID: got %q, want 'docs-bot-g3'", s.LastRunID)
	}
	if s.LastRunState != "success" {
		t.Errorf("LastRunState: got %q, want 'success'", s.LastRunState)
	}
	if len(s.Verbatim) != 4 {
		t.Fatalf("Verbatim count: got %d, want 4", len(s.Verbatim))
	}
	if s.Verbatim[0].SessionID != "ses_004" || s.Verbatim[3].SessionID != "ses_001" {
		t.Errorf("Verbatim ordering wrong: got %v / %v",
			s.Verbatim[0].SessionID, s.Verbatim[3].SessionID)
	}
	if len(s.SummaryBlocks) != 1 {
		t.Fatalf("SummaryBlocks: got %d, want 1", len(s.SummaryBlocks))
	}
	if s.SummaryBlocks[0].Label != "A" {
		t.Errorf("SummaryBlock label: got %q, want 'A'", s.SummaryBlocks[0].Label)
	}
	if s.SummaryBlocks[0].StartSession != "ses_000a" || s.SummaryBlocks[0].EndSession != "ses_000e" {
		t.Errorf("SummaryBlock range: got %s–%s",
			s.SummaryBlocks[0].StartSession, s.SummaryBlocks[0].EndSession)
	}
	if !strings.Contains(s.LastWorkedOn, "state.md parser") {
		t.Errorf("LastWorkedOn missing expected text: %q", s.LastWorkedOn)
	}
	if !strings.Contains(s.PlanningNext, "update-agent-state skill") {
		t.Errorf("PlanningNext missing expected text: %q", s.PlanningNext)
	}
	if len(s.References) != 2 {
		t.Errorf("References count: got %d, want 2", len(s.References))
	}
}

// TestWrite_RoundTrip is the canonical invariant test: a parsed
// StateMD written back to bytes must re-parse into the same
// StateMD. This guards every field — drift in either Parse or
// Write surfaces here.
func TestWrite_RoundTrip(t *testing.T) {
	original, err := agentstate.Parse(fixtureBasic())
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}

	var buf bytes.Buffer
	if err := original.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	reparsed, err := agentstate.Parse(buf.String())
	if err != nil {
		t.Fatalf("reparse: %v\n--- rendered ---\n%s", err, buf.String())
	}

	if reparsed.AgentName != original.AgentName {
		t.Errorf("AgentName drift: %q vs %q", reparsed.AgentName, original.AgentName)
	}
	if !reparsed.LastUpdated.Equal(original.LastUpdated) {
		t.Errorf("LastUpdated drift: %v vs %v", reparsed.LastUpdated, original.LastUpdated)
	}
	if len(reparsed.Verbatim) != len(original.Verbatim) {
		t.Fatalf("Verbatim count drift: %d vs %d", len(reparsed.Verbatim), len(original.Verbatim))
	}
	for i, v := range reparsed.Verbatim {
		ov := original.Verbatim[i]
		if v.SessionID != ov.SessionID || v.Summary != ov.Summary {
			t.Errorf("Verbatim[%d] drift: %+v vs %+v", i, v, ov)
		}
	}
	if len(reparsed.SummaryBlocks) != len(original.SummaryBlocks) {
		t.Fatalf("SummaryBlocks count drift: %d vs %d",
			len(reparsed.SummaryBlocks), len(original.SummaryBlocks))
	}
	if reparsed.LastWorkedOn != original.LastWorkedOn {
		t.Errorf("LastWorkedOn drift: %q vs %q", reparsed.LastWorkedOn, original.LastWorkedOn)
	}
	if reparsed.PlanningNext != original.PlanningNext {
		t.Errorf("PlanningNext drift: %q vs %q", reparsed.PlanningNext, original.PlanningNext)
	}
	if len(reparsed.References) != len(original.References) {
		t.Errorf("References count drift: %d vs %d", len(reparsed.References), len(original.References))
	}
}

// === Structural rejection tests ===

func TestParse_MissingHeader_ReturnsError(t *testing.T) {
	content := "no header here, just text\n## Session history\n"
	_, err := agentstate.Parse(content)
	if !errors.Is(err, agentstate.ErrMissingHeader) {
		t.Errorf("expected ErrMissingHeader, got %v", err)
	}
}

func TestParse_MalformedMetadata_ReturnsError(t *testing.T) {
	content := "# Agent State — alpha\n\n> **Last updated:** not-a-date **Last run:** x\n"
	_, err := agentstate.Parse(content)
	if err == nil {
		t.Fatal("expected error for non-RFC3339 timestamp")
	}
	if !strings.Contains(err.Error(), "not RFC 3339") {
		t.Errorf("error should mention RFC 3339: %v", err)
	}
}

func TestParse_TooManyVerbatim_ReturnsError(t *testing.T) {
	// 5 verbatim entries (one over cap).
	content := "# Agent State — alpha\n\n" +
		"> **Last updated:** 2026-05-18T15:32:18Z **Last run:** x · y\n\n" +
		"## Session history\n\n" +
		"### Verbatim (most recent first)\n\n" +
		"1. ses_005 · 2026-05-18 — five.\n" +
		"2. ses_004 · 2026-05-17 — four.\n" +
		"3. ses_003 · 2026-05-16 — three.\n" +
		"4. ses_002 · 2026-05-15 — two.\n" +
		"5. ses_001 · 2026-05-14 — one.\n"
	_, err := agentstate.Parse(content)
	if !errors.Is(err, agentstate.ErrTooManyVerbatim) {
		t.Errorf("expected ErrTooManyVerbatim, got %v", err)
	}
}

func TestParse_TooManySummaryBlocks_ReturnsError(t *testing.T) {
	// 4 summary blocks (one over cap).
	content := "# Agent State — alpha\n\n" +
		"> **Last updated:** 2026-05-18T15:32:18Z **Last run:** x · y\n\n" +
		"## Session history\n\n" +
		"### Verbatim (most recent first)\n\n" +
		"### Summary blocks (most recent first)\n\n" +
		"**Block A** (sessions ses_a1–ses_a5): block-a.\n" +
		"**Block B** (sessions ses_b1–ses_b5): block-b.\n" +
		"**Block C** (sessions ses_c1–ses_c5): block-c.\n" +
		"**Block D** (sessions ses_d1–ses_d5): block-d.\n"
	_, err := agentstate.Parse(content)
	if !errors.Is(err, agentstate.ErrTooManySummaryBlocks) {
		t.Errorf("expected ErrTooManySummaryBlocks, got %v", err)
	}
}

// TestParse_ASCIIDashTolerance covers the F1-style robustness gap:
// the parser must accept both em-dash "—" and ASCII "--" in the
// header AND verbatim-entry lines, mirroring the
// sessionarchive.ParseBigPicture pattern.
func TestParse_ASCIIDashTolerance(t *testing.T) {
	content := "# Agent State -- beta\n\n" +
		"> **Last updated:** 2026-05-18T15:32:18Z **Last run:** x · y\n\n" +
		"## Session history\n\n" +
		"### Verbatim (most recent first)\n\n" +
		"1. ses_001 · 2026-05-18 -- ASCII-dash variant should parse.\n"
	s, err := agentstate.Parse(content)
	if err != nil {
		t.Fatalf("ASCII dash should parse: %v", err)
	}
	if s.AgentName != "beta" {
		t.Errorf("AgentName: got %q, want 'beta'", s.AgentName)
	}
	if len(s.Verbatim) != 1 {
		t.Fatalf("Verbatim count: got %d, want 1", len(s.Verbatim))
	}
	if s.Verbatim[0].Summary != "ASCII-dash variant should parse." {
		t.Errorf("Summary: got %q", s.Verbatim[0].Summary)
	}
}

// === PromoteAndDrop tests ===

func TestPromoteAndDrop_FillsVerbatimQueue(t *testing.T) {
	s := StateMDFixture{}.empty()
	// Land 4 sessions — should fill verbatim with no graduation yet.
	for i := 1; i <= 4; i++ {
		agentstate.PromoteAndDrop(s, mkEntry(fmt.Sprintf("ses_%03d", i)))
	}
	if len(s.Verbatim) != 4 {
		t.Errorf("after 4 sessions: Verbatim count %d, want 4", len(s.Verbatim))
	}
	if len(s.SummaryBlocks) != 0 {
		t.Errorf("after 4 sessions: SummaryBlocks count %d, want 0", len(s.SummaryBlocks))
	}
	// Most-recent at slot #1.
	if s.Verbatim[0].SessionID != "ses_004" {
		t.Errorf("slot #1 should be most recent ses_004, got %q", s.Verbatim[0].SessionID)
	}
}

func TestPromoteAndDrop_GraduationOpensFirstBlock(t *testing.T) {
	s := StateMDFixture{}.empty()
	for i := 1; i <= 5; i++ {
		agentstate.PromoteAndDrop(s, mkEntry(fmt.Sprintf("ses_%03d", i)))
	}
	// After 5 sessions: 4 verbatim + 1 graduated → block A.
	if len(s.Verbatim) != 4 {
		t.Errorf("Verbatim count: got %d, want 4", len(s.Verbatim))
	}
	if len(s.SummaryBlocks) != 1 {
		t.Fatalf("SummaryBlocks count: got %d, want 1", len(s.SummaryBlocks))
	}
	if s.SummaryBlocks[0].StartSession != "ses_001" {
		t.Errorf("first block should contain graduated ses_001: %+v", s.SummaryBlocks[0])
	}
}

// TestPromoteAndDrop_30SessionYoyo verifies the spec §7.5 sliding-
// window invariant: total session count yo-yos between 15 (just
// after a drop while opening a fresh block) and 19 (peak just
// before a drop). 30 sessions exercises the full cycle past
// at least one drop.
func TestPromoteAndDrop_30SessionYoyo(t *testing.T) {
	s := StateMDFixture{}.empty()
	var minSessions, maxSessions int
	minSessions = 99 // sentinel above any realistic count

	for i := 1; i <= 30; i++ {
		agentstate.PromoteAndDrop(s, mkEntry(fmt.Sprintf("ses_%03d", i)))
		total := totalSessionCount(s)
		// Track from session 15 onward (window has stabilized).
		if i >= 15 {
			if total < minSessions {
				minSessions = total
			}
			if total > maxSessions {
				maxSessions = total
			}
			if total < agentstate.WindowFloor {
				t.Errorf("session %d: total %d below floor %d", i, total, agentstate.WindowFloor)
			}
			if total > agentstate.WindowPeak {
				t.Errorf("session %d: total %d above peak %d", i, total, agentstate.WindowPeak)
			}
		}
	}
	if minSessions != agentstate.WindowFloor {
		t.Errorf("min observed %d, want floor %d", minSessions, agentstate.WindowFloor)
	}
	if maxSessions != agentstate.WindowPeak {
		t.Errorf("max observed %d, want peak %d", maxSessions, agentstate.WindowPeak)
	}
	// After 30 sessions, never more than MaxSummaryBlocks.
	if len(s.SummaryBlocks) > agentstate.MaxSummaryBlocks {
		t.Errorf("SummaryBlocks count %d exceeds cap %d",
			len(s.SummaryBlocks), agentstate.MaxSummaryBlocks)
	}
}

// === helpers ===

// StateMDFixture is a tiny builder for empty/seeded state instances
// in the PromoteAndDrop tests. Avoids fixture boilerplate at every
// call site.
type StateMDFixture struct{}

func (StateMDFixture) empty() *agentstate.StateMD {
	return &agentstate.StateMD{AgentName: "test"}
}

func mkEntry(sessionID string) agentstate.VerbatimEntry {
	return agentstate.VerbatimEntry{
		SessionID: sessionID,
		Date:      "2026-05-18",
		Summary:   "synthetic session " + sessionID,
	}
}

// totalSessionCount tallies verbatim + summary-block entries for the
// yo-yo invariant check. Block-entry count is derived from the
// block's Summary newline count (each graduated entry adds one
// newline).
func totalSessionCount(s *agentstate.StateMD) int {
	total := len(s.Verbatim)
	for _, b := range s.SummaryBlocks {
		if b.Summary == "" {
			continue
		}
		total += strings.Count(b.Summary, "\n") + 1
	}
	return total
}
