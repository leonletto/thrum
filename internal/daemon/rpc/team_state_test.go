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
