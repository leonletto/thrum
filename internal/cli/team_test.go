package cli

import (
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
