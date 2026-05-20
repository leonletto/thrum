package rpc

import "testing"

// TestDeriveAgentState pins the §6.2 state-precedence implementation
// for B-B1 E6.8 Task 56:
//
//   - auto_respawn_disabled_at set → "crashed" (loop-guard banner
//     surfaced by Task 60 in batch 2)
//   - state_md_parse_failed_at set → "crashed" (state.md banner)
//   - active personal agent (long_lived identity) → "alive"
//   - active scheduled agent (ephemeral identity) → "idle"
//   - inactive agent → "offline"
//
// Higher-resolution states (over_budget, dispatched, working,
// idle-with-nudge-banner) need the scheduler_job_state join that
// Task 56 plumbs the next_run/last_run fields for; those derivations
// land in batch 2 when scheduler injection is wired.
func TestDeriveAgentState(t *testing.T) {
	cases := []struct {
		name string
		m    TeamMember
		want string
	}{
		{
			name: "loop-guard banner trips crashed first",
			m: TeamMember{
				AutoRespawnDisabledAt: 1747500000000,
				Status:                "active",
			},
			want: "crashed",
		},
		{
			name: "state.md banner trips crashed",
			m: TeamMember{
				StateMdParseFailedAt: 1747500000000,
				Status:               "active",
			},
			want: "crashed",
		},
		{
			name: "loop-guard precedence over state.md (both set, loop-guard checked first)",
			m: TeamMember{
				AutoRespawnDisabledAt: 1747500000000,
				StateMdParseFailedAt:  1747500000000,
				Status:                "active",
			},
			want: "crashed",
		},
		{
			name: "active personal agent (long_lived) → alive",
			m: TeamMember{
				Status:   "active",
				Identity: "long_lived",
			},
			want: "alive",
		},
		{
			name: "active scheduled agent (ephemeral) → idle",
			m: TeamMember{
				Status:   "active",
				Identity: "ephemeral",
			},
			want: "idle",
		},
		{
			name: "active with empty identity (pre-Migration-26 fixture) → alive default",
			m: TeamMember{
				Status: "active",
			},
			want: "alive",
		},
		{
			name: "inactive agent → offline",
			m: TeamMember{
				Status: "offline",
			},
			want: "offline",
		},
		{
			name: "zero-value Status (uninitialized fixture) → offline (default)",
			m:    TeamMember{}, // Status == ""
			want: "offline",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveAgentState(&tc.m)
			if got != tc.want {
				t.Errorf("deriveAgentState = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestDeriveCrashedBanner pins B-B1 E6.8 Task 60: the operator-
// facing banner strings rendered when an agent has tripped one of
// the auto-recovery guards (loop-guard or state.md corruption).
//
// Per spec §7.6, banners surface in both compact + expanded
// `thrum team` views. The exact phrasing is part of the spec —
// changes here would diverge from operator-runbook expectations,
// so the test pins the SUBSTRINGS that operators rely on
// (banner header, agent name, ack-CLI invocation) rather than the
// full string (which is more forgiving against future copy-edits
// to surrounding punctuation/wording).
func TestDeriveCrashedBanner(t *testing.T) {
	cases := []struct {
		name      string
		m         TeamMember
		wantEmpty bool
		wantSubs  []string
	}{
		{
			name: "loop-guard tripped → loop-guard banner",
			m: TeamMember{
				AgentID:               "docs_bot",
				AutoRespawnDisabledAt: 1747500000000,
			},
			wantSubs: []string{
				"AUTO-RESPAWN DISABLED",
				"docs_bot",
				"ack-respawn-alert",
			},
		},
		{
			name: "state.md unparseable → state.md banner",
			m: TeamMember{
				AgentID:              "writer_bot",
				StateMdParseFailedAt: 1747500000000,
			},
			wantSubs: []string{
				"state.md UNPARSEABLE",
				"writer_bot",
				"ack-state-corruption",
				"state.md.broken",
			},
		},
		{
			name: "both tripped → loop-guard takes precedence (matches deriveAgentState order)",
			m: TeamMember{
				AgentID:               "docs_bot",
				AutoRespawnDisabledAt: 1747500000000,
				StateMdParseFailedAt:  1747500000000,
			},
			wantSubs: []string{
				"AUTO-RESPAWN DISABLED",
				"ack-respawn-alert",
			},
		},
		{
			name:      "no flags tripped → empty banner",
			m:         TeamMember{AgentID: "docs_bot", Status: "active", Identity: "long_lived"},
			wantEmpty: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveCrashedBanner(&tc.m)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("expected empty banner; got %q", got)
				}
				return
			}
			for _, sub := range tc.wantSubs {
				if !contains(got, sub) {
					t.Errorf("banner missing %q; got %q", sub, got)
				}
			}
			// Cross-check: when state.md banner is expected but
			// loop-guard is also set, we don't want both banners
			// concatenated.
			if tc.name == "both tripped → loop-guard takes precedence (matches deriveAgentState order)" {
				if contains(got, "state.md UNPARSEABLE") {
					t.Errorf("loop-guard precedence violated: state.md banner leaked into output: %q", got)
				}
			}
		})
	}
}
