package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFormatTeam_Empty(t *testing.T) {
	result := FormatTeam(&TeamListResponse{})
	if !strings.Contains(result, "No active agents") {
		t.Errorf("expected empty state message, got: %s", result)
	}

	result = FormatTeam(&TeamListResponse{Members: []TeamMember{}})
	if !strings.Contains(result, "No active agents") {
		t.Errorf("expected empty state message, got: %s", result)
	}
}

func TestFormatTeam_SingleActive(t *testing.T) {
	now := time.Now().UTC()
	resp := &TeamListResponse{
		Members: []TeamMember{
			{
				AgentID:         "furiosa",
				Role:            "implementer",
				Module:          "auth",
				Hostname:        "macbook-pro",
				WorktreePath:    "/home/user/.workspaces/proj/feature-auth",
				SessionID:       "ses_01HXF12345678901234567890",
				SessionStart:    now.Add(-2 * time.Hour).Format(time.RFC3339Nano),
				Intent:          "JWT authentication implementation",
				InboxTotal:      12,
				InboxUnread:     3,
				Branch:          "feature/auth",
				UnmergedCommits: 3,
				FileChanges: []FileChange{
					{Path: "src/auth.go", LastModified: now.Add(-5 * time.Minute).Format(time.RFC3339Nano), Additions: 413, Deletions: 187},
					{Path: "src/auth_test.go", LastModified: now.Add(-12 * time.Minute).Format(time.RFC3339Nano), Additions: 89, Deletions: 12},
				},
				Status: "active",
			},
		},
	}

	result := FormatTeam(resp)

	// Check header (compact summary format)
	if !strings.Contains(result, "● @furiosa (auth)") {
		t.Errorf("missing header, got: %s", result)
	}

	// Check worktree and host as separate fields
	if !strings.Contains(result, "Worktree: feature-auth") {
		t.Errorf("missing worktree, got: %s", result)
	}
	if !strings.Contains(result, "Host:     macbook-pro") {
		t.Errorf("missing host, got: %s", result)
	}

	// Check session with truncation (16 chars + "...")
	if !strings.Contains(result, "ses_01HXF1234567...") {
		t.Errorf("missing truncated session ID, got: %s", result)
	}
	if !strings.Contains(result, "active") {
		t.Errorf("missing active duration, got: %s", result)
	}

	// Check intent
	if !strings.Contains(result, "Intent:   JWT authentication implementation") {
		t.Errorf("missing intent, got: %s", result)
	}

	// Check inbox
	if !strings.Contains(result, "3 unread / 12 total") {
		t.Errorf("missing inbox counts, got: %s", result)
	}

	// Check branch
	if !strings.Contains(result, "feature/auth (3 commits ahead)") {
		t.Errorf("missing branch info, got: %s", result)
	}

	// Check files
	if !strings.Contains(result, "src/auth.go") {
		t.Errorf("missing file change, got: %s", result)
	}
	if !strings.Contains(result, "+413") {
		t.Errorf("missing additions count, got: %s", result)
	}
}

func TestFormatTeam_Offline(t *testing.T) {
	resp := &TeamListResponse{
		Members: []TeamMember{
			{
				AgentID:    "reviewer",
				Role:       "reviewer",
				Module:     "all",
				Hostname:   "server",
				LastSeen:   time.Now().Add(-3 * time.Hour).Format(time.RFC3339),
				InboxTotal: 5,
				Status:     "offline",
			},
		},
	}

	result := FormatTeam(resp)

	if !strings.Contains(result, "Session:  offline") {
		t.Errorf("expected offline session, got: %s", result)
	}
	if !strings.Contains(result, "last seen") {
		t.Errorf("expected last seen info, got: %s", result)
	}
}

func TestFormatTeam_NoChanges(t *testing.T) {
	resp := &TeamListResponse{
		Members: []TeamMember{
			{
				AgentID:      "reviewer",
				Role:         "reviewer",
				Module:       "all",
				SessionID:    "ses_test",
				SessionStart: time.Now().Add(-10 * time.Minute).Format(time.RFC3339),
				Branch:       "main",
				Status:       "active",
			},
		},
	}

	result := FormatTeam(resp)

	if !strings.Contains(result, "(no changes)") {
		t.Errorf("expected '(no changes)', got: %s", result)
	}
}

func TestFormatTeam_Multiple(t *testing.T) {
	now := time.Now().UTC()
	resp := &TeamListResponse{
		Members: []TeamMember{
			{
				AgentID:      "agent1",
				Role:         "implementer",
				Module:       "auth",
				SessionID:    "ses_1",
				SessionStart: now.Add(-1 * time.Hour).Format(time.RFC3339),
				Status:       "active",
			},
			{
				AgentID:      "agent2",
				Role:         "planner",
				Module:       "core",
				SessionID:    "ses_2",
				SessionStart: now.Add(-30 * time.Minute).Format(time.RFC3339),
				Status:       "active",
			},
		},
	}

	result := FormatTeam(resp)

	// Should have both headers
	if !strings.Contains(result, "@agent1") {
		t.Errorf("missing agent1, got: %s", result)
	}
	if !strings.Contains(result, "@agent2") {
		t.Errorf("missing agent2, got: %s", result)
	}

	// Should have blank line between members
	parts := strings.Split(result, "● @agent2")
	if len(parts) < 2 {
		t.Errorf("missing agent2 block, got: %s", result)
	}
}

func TestFormatTeam_LocationVariants(t *testing.T) {
	tests := []struct {
		name         string
		hostname     string
		worktree     string
		wantWorktree string
		wantHost     string
	}{
		{"both", "macbook", "/path/to/feature-auth", "Worktree: feature-auth", "Host:     macbook"},
		{"hostname_only", "server", "", "", "Host:     server"},
		{"worktree_only", "", "/path/to/my-branch", "Worktree: my-branch", ""},
		{"neither", "", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &TeamListResponse{
				Members: []TeamMember{{
					AgentID:      "test",
					Role:         "tester",
					Module:       "test",
					Hostname:     tt.hostname,
					WorktreePath: tt.worktree,
					SessionID:    "ses_test",
					SessionStart: time.Now().Format(time.RFC3339),
					Status:       "active",
				}},
			}

			result := FormatTeam(resp)
			if tt.wantWorktree != "" {
				if !strings.Contains(result, tt.wantWorktree) {
					t.Errorf("expected %q, got: %s", tt.wantWorktree, result)
				}
			}
			if tt.wantHost != "" {
				if !strings.Contains(result, tt.wantHost) {
					t.Errorf("expected %q, got: %s", tt.wantHost, result)
				}
			}
			if tt.wantWorktree == "" && tt.wantHost == "" {
				if strings.Contains(result, "Worktree:") || strings.Contains(result, "Host:") {
					t.Errorf("should not have Worktree/Host lines, got: %s", result)
				}
			}
		})
	}
}

func TestFormatTeam_SharedMessages(t *testing.T) {
	t.Run("broadcasts_only", func(t *testing.T) {
		resp := &TeamListResponse{
			Members: []TeamMember{{
				AgentID:   "agent1",
				Role:      "implementer",
				Module:    "auth",
				SessionID: "ses_1",
				Status:    "active",
			}},
			SharedMessages: &SharedMessages{
				BroadcastTotal: 42,
			},
		}

		result := FormatTeam(resp)
		if !strings.Contains(result, "--- Shared ---") {
			t.Errorf("missing shared header, got: %s", result)
		}
		if !strings.Contains(result, "Broadcasts: 42 messages") {
			t.Errorf("missing broadcast count, got: %s", result)
		}
	})

	t.Run("broadcasts_and_groups", func(t *testing.T) {
		resp := &TeamListResponse{
			Members: []TeamMember{{
				AgentID:   "agent1",
				Role:      "implementer",
				Module:    "auth",
				SessionID: "ses_1",
				Status:    "active",
			}},
			SharedMessages: &SharedMessages{
				BroadcastTotal: 10,
				Groups: []GroupMessageCount{
					{Name: "reviewers", Total: 12},
					{Name: "everyone", Total: 8},
				},
			},
		}

		result := FormatTeam(resp)
		if !strings.Contains(result, "Broadcasts: 10 messages") {
			t.Errorf("missing broadcast count, got: %s", result)
		}
		if !strings.Contains(result, "@reviewers: 12 messages") {
			t.Errorf("missing reviewers group, got: %s", result)
		}
		if !strings.Contains(result, "@everyone: 8 messages") {
			t.Errorf("missing everyone group, got: %s", result)
		}
	})

	t.Run("no_shared_messages", func(t *testing.T) {
		resp := &TeamListResponse{
			Members: []TeamMember{{
				AgentID:   "agent1",
				Role:      "implementer",
				Module:    "auth",
				SessionID: "ses_1",
				Status:    "active",
			}},
		}

		result := FormatTeam(resp)
		if strings.Contains(result, "--- Shared ---") {
			t.Errorf("should not have shared section when no shared messages, got: %s", result)
		}
	})
}

func TestFormatTeam_LiveIndicator(t *testing.T) {
	resp := &TeamListResponse{
		Members: []TeamMember{
			{
				AgentID:  "test_agent",
				Role:     "implementer",
				Module:   "test",
				AgentPID: os.Getpid(), // current process = alive
				Status:   "active",
			},
		},
	}
	output := FormatTeam(resp)
	if !strings.Contains(output, "[live]") {
		t.Errorf("expected [live] indicator, got: %s", output)
	}
}

func TestFormatTeam_StaleIndicator(t *testing.T) {
	resp := &TeamListResponse{
		Members: []TeamMember{
			{
				AgentID:  "test_agent",
				Role:     "implementer",
				Module:   "test",
				AgentPID: 999999, // dead PID
				Status:   "active",
			},
		},
	}
	output := FormatTeam(resp)
	if !strings.Contains(output, "[stale]") {
		t.Errorf("expected [stale] indicator, got: %s", output)
	}
}

func TestFormatTeam_NoPIDNoIndicator(t *testing.T) {
	resp := &TeamListResponse{
		Members: []TeamMember{
			{
				AgentID:  "test_agent",
				Role:     "implementer",
				Module:   "test",
				AgentPID: 0, // no PID
				Status:   "active",
			},
		},
	}
	output := FormatTeam(resp)
	if strings.Contains(output, "[live]") || strings.Contains(output, "[stale]") {
		t.Errorf("expected no PID indicator for zero PID, got: %s", output)
	}
}

func TestFormatTeam_ShowsRuntime(t *testing.T) {
	resp := &TeamListResponse{
		Members: []TeamMember{{
			AgentID: "test_agent",
			Role:    "tester",
			Module:  "unit",
			Status:  "active",
			Runtime: "codex",
		}},
	}
	out := FormatTeam(resp)
	if !strings.Contains(out, "Runtime:  codex") {
		t.Errorf("FormatTeam output missing 'Runtime:  codex' line, got:\n%s", out)
	}
}

func TestFormatTeam_OmitsRuntimeWhenEmpty(t *testing.T) {
	resp := &TeamListResponse{
		Members: []TeamMember{{
			AgentID: "test_agent",
			Role:    "tester",
			Module:  "unit",
			Status:  "active",
		}},
	}
	out := FormatTeam(resp)
	if strings.Contains(out, "Runtime:") {
		t.Errorf("FormatTeam output should not contain 'Runtime:' when field is empty:\n%s", out)
	}
}

// TestTeamMember_JSONRoundtripPreservesOriginDaemon verifies that the CLI
// TeamMember struct carries origin_daemon across an unmarshal → marshal
// roundtrip. Regression for thrum-ti9e: the daemon's team.list response
// emitted origin_daemon, but the CLI's TeamMember struct had no such
// field, so json.Unmarshal silently dropped it and the re-marshaled JSON
// output (`thrum team --all --json`) never surfaced it to consumers. The
// fix added the field; this test pins it.
func TestTeamMember_JSONRoundtripPreservesOriginDaemon(t *testing.T) {
	daemonResponse := []byte(`{
		"members": [{
			"agent_id": "test_a",
			"role": "implementer",
			"module": "auth",
			"origin_daemon": "d_local_01",
			"worktree": "/tmp/a",
			"status": "active",
			"unmerged_commits": 0,
			"inbox_total": 0,
			"inbox_unread": 0
		}, {
			"agent_id": "test_b",
			"role": "reviewer",
			"module": "all",
			"origin_daemon": "d_peer_02",
			"status": "active",
			"unmerged_commits": 0,
			"inbox_total": 0,
			"inbox_unread": 0
		}]
	}`)

	var resp TeamListResponse
	if err := json.Unmarshal(daemonResponse, &resp); err != nil {
		t.Fatalf("unmarshal daemon response: %v", err)
	}
	if len(resp.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(resp.Members))
	}

	// Struct-level: fields must be populated, not silently dropped.
	if resp.Members[0].OriginDaemon != "d_local_01" {
		t.Errorf("member[0].OriginDaemon = %q, want d_local_01", resp.Members[0].OriginDaemon)
	}
	if resp.Members[1].OriginDaemon != "d_peer_02" {
		t.Errorf("member[1].OriginDaemon = %q, want d_peer_02", resp.Members[1].OriginDaemon)
	}

	// Re-marshal (what `thrum team --all --json` actually outputs).
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	var raw struct {
		Members []map[string]any `json:"members"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal re-serialized: %v", err)
	}
	for i, want := range []string{"d_local_01", "d_peer_02"} {
		got, ok := raw.Members[i]["origin_daemon"]
		if !ok || got != want {
			t.Errorf("re-marshaled member[%d].origin_daemon = %v (present=%v), want %q",
				i, got, ok, want)
		}
	}
}

// --- reminders block (thrum-6qmf.3.4) ---

func TestFormatTeamReminders_EmptyHidesBlock(t *testing.T) {
	if got := formatTeamReminders(nil); got != "" {
		t.Errorf("nil ids should render empty; got %q", got)
	}
	if got := formatTeamReminders([]string{}); got != "" {
		t.Errorf("empty ids should render empty; got %q", got)
	}
}

func TestFormatTeamReminders_RendersMultiLine(t *testing.T) {
	ids := []string{
		"reminder-docs_bot-100-0001",
		"reminder-docs_bot-200-1111",
	}
	got := formatTeamReminders(ids)
	if !strings.HasPrefix(got, "Reminders:\n") {
		t.Errorf("missing header; got %q", got)
	}
	for _, id := range ids {
		if !strings.Contains(got, "  "+id+"\n") {
			t.Errorf("id %q missing or wrong indent: %s", id, got)
		}
	}
}

func TestFormatTeamReminders_PreservesOrder(t *testing.T) {
	ids := []string{"reminder-x-3-3", "reminder-x-1-1", "reminder-x-2-2"}
	got := formatTeamReminders(ids)
	// Order from the daemon must be preserved end-to-end (the daemon
	// orders by next_reminder_at; resorting in the CLI would break that).
	expectOrder := []int{
		strings.Index(got, "reminder-x-3-3"),
		strings.Index(got, "reminder-x-1-1"),
		strings.Index(got, "reminder-x-2-2"),
	}
	for i := 1; i < len(expectOrder); i++ {
		if expectOrder[i-1] >= expectOrder[i] {
			t.Errorf("order broken between index %d and %d: %v", i-1, i, expectOrder)
		}
	}
}

func TestFormatTeamReminders_PassesThroughMoreMarker(t *testing.T) {
	ids := []string{"reminder-x-1-1", "reminder-x-2-2", "... +5 more"}
	got := formatTeamReminders(ids)
	if !strings.Contains(got, "... +5 more") {
		t.Errorf("synthetic '... +N more' marker should pass through: %s", got)
	}
}

func TestFormatTeam_AgentWithReminders(t *testing.T) {
	now := time.Now().UTC()
	resp := &TeamListResponse{
		Members: []TeamMember{{
			AgentID:      "docs_bot",
			Role:         "implementer",
			Module:       "docs",
			Status:       "active",
			SessionID:    "ses_x",
			SessionStart: now.Format(time.RFC3339),
			Reminders: []string{
				"reminder-docs_bot-100-0001",
				"reminder-docs_bot-200-1111",
			},
		}},
	}
	out := FormatTeam(resp)
	if !strings.Contains(out, "Reminders:") {
		t.Errorf("expected Reminders block in output:\n%s", out)
	}
	if !strings.Contains(out, "reminder-docs_bot-100-0001") {
		t.Errorf("expected reminder id in output:\n%s", out)
	}
}

func TestFormatTeam_AgentWithoutReminders_NoBlock(t *testing.T) {
	now := time.Now().UTC()
	resp := &TeamListResponse{
		Members: []TeamMember{{
			AgentID:      "docs_bot",
			Role:         "implementer",
			Module:       "docs",
			Status:       "active",
			SessionID:    "ses_x",
			SessionStart: now.Format(time.RFC3339),
		}},
	}
	out := FormatTeam(resp)
	if strings.Contains(out, "Reminders:") {
		t.Errorf("agent without reminders should not render the Reminders block; got:\n%s", out)
	}
}

// TestFormatContextColumn pins the rendering matrix for the
// CR.6 / thrum-6qmf.1.21 context column. Layout per plan §3.4
// (see the formatContextColumn docstring for the full table).
func TestFormatContextColumn(t *testing.T) {
	cases := []struct {
		name   string
		pct    int
		approx bool
		known  bool
		want   string
	}{
		{"unknown_when_not_known", 0, false, false, "unknown"},
		{"unknown_overrides_pct", 80, true, false, "unknown"},
		{"direct_below_warn", 50, false, true, "50% ctx"},
		{"direct_at_warn", 70, false, true, "70% ctx ⚠"},
		{"direct_above_warn_below_auto", 75, false, true, "75% ctx ⚠"},
		{"direct_at_auto", 80, false, true, "80% ctx 🔥"},
		{"direct_above_auto", 95, false, true, "95% ctx 🔥"},
		{"approx_below_warn", 50, true, true, "~50% ctx"},
		{"approx_at_warn", 70, true, true, "~70% ctx ⚠"},
		{"approx_at_auto", 85, true, true, "~85% ctx 🔥"},
		{"zero_percent_is_known", 0, false, true, "0% ctx"},
		{"zero_percent_approx_is_known", 0, true, true, "~0% ctx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatContextColumn(tc.pct, tc.approx, tc.known)
			if got != tc.want {
				t.Errorf("formatContextColumn(%d, approx=%v, known=%v) = %q, want %q",
					tc.pct, tc.approx, tc.known, got, tc.want)
			}
		})
	}
}

// TestFormatTeam_ContextLineAlwaysRenders confirms FormatTeam emits
// the Context line for every agent in the listing, including agents
// with ContextKnown=false (rendered "unknown"). Per plan §3.4 the
// column is always-on so operators can spot agents whose runtime
// has no parser registered.
func TestFormatTeam_ContextLineAlwaysRenders(t *testing.T) {
	resp := &TeamListResponse{
		Members: []TeamMember{
			{
				AgentID:      "impl_known",
				Module:       "test",
				Status:       "active",
				ContextPct:   55,
				ContextKnown: true,
			},
			{
				AgentID: "impl_unknown",
				Module:  "test",
				Status:  "active",
				// ContextKnown left false — no parser for this agent.
			},
		},
	}
	out := FormatTeam(resp)

	if want := "Context:  55% ctx"; !strings.Contains(out, want) {
		t.Errorf("FormatTeam missing %q for known agent; got:\n%s", want, out)
	}
	if want := "Context:  unknown"; !strings.Contains(out, want) {
		t.Errorf("FormatTeam missing %q for unknown agent; got:\n%s", want, out)
	}
}
