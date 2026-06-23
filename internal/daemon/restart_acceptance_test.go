package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/types"
)

// TestRestartAcceptance_DeadAgentClass is the thrum-5oui broad
// restart-acceptance guard. It exercises the agent-restart failure class
// across agent KINDS (implementer, researcher) and STATES:
//
//   - killed-live: the runtime died AFTER its session started (dead PID +
//     still-active session) — the live DeadAgentSweeper-cull route (route 3,
//     proven by researcher_memories culled 17:48:44 on rc.6).
//   - idle-no-session: a registered agent whose session already ended (dead
//     PID, no active session) — the fresh/idle-start chicken-and-egg (route 4,
//     substrate-ui-research).
//
// It pins the two invariants that together make restart work for the WHOLE
// class, regardless of how the worktree lost its active session:
//
//  1. KEYSTONE (universal): tmux.create is anonymous-allowed, so an UNBOUND
//     caller in the dead agent's worktree can invoke the `thrum tmux start`
//     bootstrap at all. This is the single enabler that closes both routes —
//     binding is no longer a precondition for creating the session that
//     re-establishes binding.
//  2. DEFENSE-IN-DEPTH (killed-live only): after a sweep, the sweeper ends the
//     dead agent's session AND resets agents.agent_pid to 0 — the
//     sweeper-skipped, restartable sentinel — so it is not re-culled and boot
//     reconcile (thrum-mnhp) keeps it bindable. (An idle agent has no active
//     session, so it is not a sweep candidate; its restartability rests purely
//     on the keystone.)
//
// Worktree path-form is intentionally NOT a row here: matchWorktree
// EvalSymlinks BOTH the candidate and the stored path (resolver.go), and that
// axis is covered by the peercred resolver tests; it was refuted as a class
// member during root-cause.
func TestRestartAcceptance_DeadAgentClass(t *testing.T) {
	// Invariant 1 — the keystone — is global and gates the whole class.
	// `thrum tmux start` runs tmux.create THEN tmux.launch on one connection
	// (a no_agent create writes no session_ref, so the caller stays anonymous
	// for launch); BOTH must be anonymous-allowed or the bootstrap half-runs.
	for _, m := range []string{"tmux.create", "tmux.launch"} {
		if !anonymousAllowedMethods[m] {
			t.Fatalf("KEYSTONE MISSING (thrum-5oui): %s not in anonymousAllowedMethods — "+
				"an unbound caller cannot complete the `thrum tmux start` bootstrap; the restart class is broken.", m)
		}
	}

	cases := []struct {
		name       string
		role       string
		killedLive bool // true: dead pid + active session; false: dead pid + ended session
	}{
		{"implementer_killed_live", "implementer", true},
		{"implementer_idle_no_session", "implementer", false},
		{"researcher_killed_live", "researcher", true},
		{"researcher_idle_no_session", "researcher", false},
	}

	const deadPID = 999999
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := createTestStateForSync(t)
			ctx := context.Background()
			agentID := "restart_" + tc.name
			sessionID := "ses_" + tc.name
			now := func() string { return time.Now().UTC().Format(time.RFC3339Nano) }

			if _, err := st.WriteEvent(ctx, types.AgentRegisterEvent{
				Type: "agent.register", Timestamp: now(), EventID: "evt_reg_" + tc.name,
				AgentID: agentID, Kind: "agent", Role: tc.role, Module: "restart-accept",
				AgentPID: deadPID,
			}); err != nil {
				t.Fatalf("register: %v", err)
			}
			if _, err := st.WriteEvent(ctx, types.AgentSessionStartEvent{
				Type: "agent.session.start", Timestamp: now(), EventID: "evt_ses_" + tc.name,
				SessionID: sessionID, AgentID: agentID,
			}); err != nil {
				t.Fatalf("session.start: %v", err)
			}
			if !tc.killedLive {
				// idle: the session already ended before this restart attempt.
				if _, err := st.WriteEvent(ctx, types.AgentSessionEndEvent{
					Type: "agent.session.end", Timestamp: now(), SessionID: sessionID,
					Reason: "test_idle_setup",
				}); err != nil {
					t.Fatalf("session.end (idle setup): %v", err)
				}
			}

			NewDeadAgentSweeper(st, "").Sweep(ctx)

			var pid int
			if err := st.RawDB().QueryRow(
				"SELECT agent_pid FROM agents WHERE agent_id = ?", agentID).Scan(&pid); err != nil {
				t.Fatalf("read agent_pid: %v", err)
			}

			if tc.killedLive {
				// Invariant 2: the live sweeper-cull route must leave the agent
				// restartable — session ended AND pid reset to 0.
				if pid != 0 {
					t.Errorf("killed-live %s: agent_pid = %d after sweep, want 0 "+
						"(sweeper must zero a dead pid so the worktree stays restartable)", tc.role, pid)
				}
				var active int
				if err := st.RawDB().QueryRow(
					"SELECT COUNT(*) FROM sessions WHERE agent_id = ? AND ended_at IS NULL", agentID).Scan(&active); err != nil {
					t.Fatalf("count active sessions: %v", err)
				}
				if active != 0 {
					t.Errorf("killed-live %s: %d active sessions after sweep, want 0 (dead runtime's session must be ended)", tc.role, active)
				}
			} else {
				// idle: not a sweep candidate (no active session); the sweeper
				// leaves it untouched. Restartability here rests on the keystone
				// (asserted globally above) + boot reconcile (thrum-mnhp). We
				// only assert the sweeper did NOT spuriously act.
				var active int
				if err := st.RawDB().QueryRow(
					"SELECT COUNT(*) FROM sessions WHERE agent_id = ? AND ended_at IS NULL", agentID).Scan(&active); err != nil {
					t.Fatalf("count active sessions: %v", err)
				}
				if active != 0 {
					t.Errorf("idle %s: expected no active session in the idle fixture, got %d", tc.role, active)
				}
			}
		})
	}
}
