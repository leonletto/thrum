package cli

import (
	"strings"
	"testing"
)

// TestFormatTeamExpanded_RendersAllLifecycleFields confirms the
// expanded single-agent view surfaces the spec §7.6 lifecycle fields
// (mode, identity, banner, recent transitions, body) in their
// documented order: banner first (highest-signal), then identity
// columns, then runtime state, then transitions, then body fallback.
func TestFormatTeamExpanded_RendersAllLifecycleFields(t *testing.T) {
	m := &TeamMember{
		AgentID:           "docs_bot",
		Module:            "writer",
		Status:            "active",
		Intent:            "drafting weekly digest",
		Branch:            "feature/docs",
		Mode:              "persistent",
		Identity:          "long_lived",
		NextRun:           "2026-05-19T08:00:00Z",
		LastRun:           "2026-05-18T08:00:00Z",
		LastRunState:      "completed",
		RecentTransitions: []string{"2026-05-18T08:00:00Z · respawn_fired · pane gone"},
		Banner:            "⚠ AUTO-RESPAWN DISABLED — 3 respawns in 600 seconds tripped the loop guard.",
		Body:              "Last run: completed at 2026-05-18T08:00:00Z. No summary.",
	}
	got := FormatTeamExpanded(m)

	mustContain := []string{
		"docs_bot",
		"AUTO-RESPAWN DISABLED",
		"Mode:",
		"persistent",
		"Identity:",
		"long_lived",
		"NextRun:",
		"LastRun:",
		"completed",
		"Recent:",
		"respawn_fired",
		"What's happening:",
		"No summary",
	}
	for _, sub := range mustContain {
		if !strings.Contains(got, sub) {
			t.Errorf("expanded view missing %q\n---got---\n%s", sub, got)
		}
	}

	// Banner must appear before lifecycle metadata (operator reads
	// the alert first per spec §7.6).
	bannerIdx := strings.Index(got, "AUTO-RESPAWN DISABLED")
	modeIdx := strings.Index(got, "Mode:")
	if bannerIdx == -1 || modeIdx == -1 || bannerIdx > modeIdx {
		t.Errorf("Banner must precede lifecycle metadata; banner@%d mode@%d", bannerIdx, modeIdx)
	}

	// Body must appear after transitions (chain rendered at bottom).
	bodyIdx := strings.Index(got, "What's happening:")
	transitionsIdx := strings.Index(got, "Recent:")
	if bodyIdx == -1 || transitionsIdx == -1 || bodyIdx < transitionsIdx {
		t.Errorf("Body must follow transitions; body@%d transitions@%d", bodyIdx, transitionsIdx)
	}
}

// TestFormatTeamExpanded_BodyOmittedWhenEmpty confirms the expanded
// view does not emit a "What's happening:" header when the daemon
// hasn't populated Body — avoids a misleading empty section in
// fixtures that pre-date the body wiring.
func TestFormatTeamExpanded_BodyOmittedWhenEmpty(t *testing.T) {
	m := &TeamMember{
		AgentID: "docs_bot",
		Module:  "writer",
		Status:  "active",
		Mode:    "persistent",
		Body:    "",
	}
	got := FormatTeamExpanded(m)
	if strings.Contains(got, "What's happening:") {
		t.Errorf("empty Body must omit section header; got:\n%s", got)
	}
}

// TestFormatTeamExpanded_BodyMultilineIndented confirms the body
// fallback chain (which may be multiple lines for the live pane
// snippet branch) is indented per the existing "Recent:" / Files:
// rendering convention so the section reads as a coherent block.
func TestFormatTeamExpanded_BodyMultilineIndented(t *testing.T) {
	m := &TeamMember{
		AgentID: "docs_bot",
		Status:  "active",
		Body:    "first line\nsecond line\nthird line",
	}
	got := FormatTeamExpanded(m)
	if !strings.Contains(got, "  first line") {
		t.Errorf("body line 1 must be indented; got:\n%s", got)
	}
	if !strings.Contains(got, "  third line") {
		t.Errorf("body line 3 must be indented; got:\n%s", got)
	}
}

// TestFormatJournalSection_RendersWithHeader confirms the journal
// payload from the daemon is wrapped in a "Journal (last events):"
// header so the operator can locate it within the expanded view.
func TestFormatJournalSection_RendersWithHeader(t *testing.T) {
	resp := &JournalResponse{
		AgentName: "docs_bot",
		Journal:   "2026-05-18T12:00:00Z · respawn_fired · pane gone\n2026-05-17T18:00:00Z · crash_detected · unhandled exception\n",
	}
	got := FormatJournalSection(resp)
	mustContain := []string{
		"Journal (last events):",
		"respawn_fired",
		"crash_detected",
	}
	for _, sub := range mustContain {
		if !strings.Contains(got, sub) {
			t.Errorf("journal section missing %q\n---got---\n%s", sub, got)
		}
	}
}

// TestFormatJournalSection_EmptyJournal_NoSection confirms an empty
// journal payload renders to an empty string so the CLI doesn't emit
// a dangling header with no body when the agent has no lifecycle
// events.
func TestFormatJournalSection_EmptyJournal_NoSection(t *testing.T) {
	resp := &JournalResponse{AgentName: "docs_bot", Journal: ""}
	if got := FormatJournalSection(resp); got != "" {
		t.Errorf("empty journal must produce empty section; got %q", got)
	}
}

// TestFormatFilesSection_RPCUnavailable confirms the operator sees
// the canonical "files RPC unavailable in this daemon" message when
// the cross-epic MB-1.S2 Q10 RPC isn't registered.
func TestFormatFilesSection_RPCUnavailable(t *testing.T) {
	got := FormatFilesSection(nil, false)
	if !strings.Contains(got, FilesRPCUnavailable) {
		t.Errorf("unavailable section missing canonical message; got %q", got)
	}
}

// TestFormatFilesSection_EmptyPaths_NoneMarker confirms an available
// RPC with zero paths renders a "(none)" placeholder so the section
// still indicates the agent's state folder was inspected (vs the
// "unavailable" path which means the RPC didn't run at all).
func TestFormatFilesSection_EmptyPaths_NoneMarker(t *testing.T) {
	got := FormatFilesSection(nil, true)
	if !strings.Contains(got, "(none)") {
		t.Errorf("available-but-empty must render (none); got %q", got)
	}
}

// TestFormatFilesSection_RendersPaths confirms file paths surface
// indented under the Files: header so multi-file output stays
// readable in the expanded view.
func TestFormatFilesSection_RendersPaths(t *testing.T) {
	got := FormatFilesSection([]string{"summary.md", "state.md"}, true)
	if !strings.Contains(got, "Files:") {
		t.Errorf("files section missing header; got %q", got)
	}
	if !strings.Contains(got, "  summary.md") || !strings.Contains(got, "  state.md") {
		t.Errorf("files paths must be indented; got %q", got)
	}
}
