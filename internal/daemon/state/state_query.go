package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// IsAgentActive returns true iff an agent with the given agent_id has a
// current (non-ended) session row. The name parameter matches the
// agents.agent_id column, which for unnamed agents looks like
// "<role>_<suffix>" and for named agents is the chosen name.
//
// Mirrors the active-agent check used by team.list and the RPC agent
// resurrect path in internal/daemon/rpc/agent.go.
func (s *State) IsAgentActive(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			  FROM agents a
			  JOIN sessions s ON s.agent_id = a.agent_id
			 WHERE a.agent_id = ?
			   AND s.ended_at IS NULL
		)`, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("is_agent_active: %w", err)
	}
	return exists, nil
}

// ListActiveAgentsByRole returns the agent IDs of all agents with the
// given role that currently have at least one non-ended session row.
// Mirrors the SQL pattern in internal/groups/resolver.go:queryAgentsByRole,
// but lives on *State because State owns the agents/sessions projection.
func (s *State) ListActiveAgentsByRole(ctx context.Context, role string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT a.agent_id
		  FROM agents a
		  JOIN sessions s ON s.agent_id = a.agent_id
		 WHERE a.role = ?
		   AND s.ended_at IS NULL`, role)
	if err != nil {
		return nil, fmt.Errorf("list_active_agents_by_role: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan agent_id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return out, nil
}

// ListAgentsInWorktree returns the agent IDs of every agent that has
// ever had a session_ref mapping them to the given worktree path.
// Ended sessions are included deliberately — an agent whose session
// is temporarily ended is still a legitimate co-located agent and its
// identity file must not be quarantined by enforceWorktreeIdentity.
// Path comparison mirrors peercred's symlink canonicalization.
func (s *State) ListAgentsInWorktree(ctx context.Context, worktree string) []string {
	if worktree == "" {
		return nil
	}
	target := canonWorktreePath(worktree)

	// No lock taken here. The *State read-helpers in this file
	// (IsAgentActive, ListActiveAgentsByRole) follow the same
	// convention: callers may or may not already hold s.Lock(), and
	// taking RLock here would deadlock HandleRegister's
	// enforceWorktreeIdentity hook (which runs under the outer write
	// lock). Serialization against writers comes from the
	// single-connection pool — NewState opens the DB with
	// SetMaxOpenConns(1) (see internal/schema/schema.go), so SQLite
	// operations are linearised by the pool regardless of Go-level
	// locking.
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT s.agent_id, sr.ref_value
		  FROM session_refs sr
		  JOIN sessions s ON sr.session_id = s.session_id
		 WHERE sr.ref_type = 'worktree'
		   AND sr.ref_value IS NOT NULL
		   AND sr.ref_value != ''`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	seen := make(map[string]struct{})
	for rows.Next() {
		var agentID, wt string
		if scanErr := rows.Scan(&agentID, &wt); scanErr != nil {
			continue
		}
		if canonWorktreePath(wt) != target {
			continue
		}
		seen[agentID] = struct{}{}
	}
	// Matches the rows.Err() convention in sibling helpers
	// (ListActiveAgentsByRole): silently drop the partial result on
	// driver error so HandleRegister's enforcement path stays safe
	// rather than preserving nothing.
	if err := rows.Err(); err != nil {
		return nil
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

// IsAgentInWorktree reports whether the given agent_id has ever been
// registered with a session_ref mapping it to the given worktree path.
// Active-session filtering is deliberately NOT applied: an agent whose
// current session is temporarily ended (between session end and the
// next session start) is still a legitimate owner of that worktree,
// and DaemonResolve needs to trust its CLI claim during that window.
//
// thrum-0pos shared-worktree disambiguation: peercred resolves a
// connecting process to one agent via CWD → git-root → registered
// worktree, but when multiple agents share a worktree the pick is
// arbitrary. DaemonResolve uses this predicate to validate that a CLI-
// asserted CallerAgentID is a legitimate co-located agent (not a
// cross-worktree forgery) before trusting it on an identity_mismatch.
//
// Path comparison uses the same canonicalization as peercred's
// matchWorktree (filepath.EvalSymlinks with Clean fallback) so a DB
// row stored as "/tmp/foo" matches a peercred-resolved worktree of
// "/private/tmp/foo" on macOS.
//
// Tradeoff: an agent that moved to a different worktree will still
// historically match the old one via residual session_refs. The
// narrower active-session filter rejected legitimate temporary
// session-gaps (see SC-10 which session.end's between assertions)
// and was the wrong axis to gate on. Forgery defense still holds
// because the claim must match a HISTORICAL session_ref for this
// agent at this worktree; an attacker in an unrelated worktree
// cannot satisfy that without having been registered there.
func (s *State) IsAgentInWorktree(ctx context.Context, agentID, worktree string) bool {
	if agentID == "" || worktree == "" {
		return false
	}
	target := canonWorktreePath(worktree)

	// No lock taken here. DaemonResolve invokes this predicate from
	// both lock-free and write-locked paths; an RLock here would
	// deadlock against queued writers on the write-locked path.
	// Serialization against writers comes from the single-connection
	// pool — NewState opens the DB with SetMaxOpenConns(1) (see
	// internal/schema/db.go), so SQLite operations are linearised
	// by the pool regardless of Go-level locking. Matches the
	// convention used by the other state_query.go read helpers
	// (IsAgentActive, ListActiveAgentsByRole).
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT sr.ref_value
		  FROM session_refs sr
		  JOIN sessions s ON sr.session_id = s.session_id
		 WHERE s.agent_id = ?
		   AND sr.ref_type = 'worktree'
		   AND sr.ref_value IS NOT NULL
		   AND sr.ref_value != ''`, agentID)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var wt string
		if scanErr := rows.Scan(&wt); scanErr != nil {
			continue
		}
		if canonWorktreePath(wt) == target {
			return true
		}
	}

	// Fallback: the agent may have been registered into this worktree
	// via `thrum agent register` without starting a session yet, in
	// which case there is no session_ref row yet but the CLI has
	// written <worktree>/.thrum/identities/<agentID>.json via the
	// CLI-side SaveIdentityFile call. An identity file at the
	// peercred-verified worktree is itself a strong ownership signal —
	// the caller's kernel-verified CWD corroborates it. This closes
	// the AN-10-style gap where a fresh register is immediately
	// followed by session.start before any session_ref exists.
	//
	// Limitation: this assumes named agents (agentID is the filename
	// base). For unnamed agents, SaveIdentityFile writes
	// role_module.json and the identity-file fallback will not match.
	// In practice every agent this predicate authenticates is named —
	// unnamed agents never call into the shared-worktree fallback
	// because the CLI only forwards CallerAgentID when the identity
	// file has a Name. Named-only coverage is acceptable for the
	// short-term 0pos fix; the enlw.9 ListActiveAgentRows rewrite
	// will replace this whole helper.
	idPath := filepath.Join(worktree, ".thrum", "identities", agentID+".json")
	if _, err := os.Stat(idPath); err == nil {
		return true
	}
	return false
}

// canonWorktreePath canonicalizes a worktree path the same way peercred's
// matchWorktree does: filepath.EvalSymlinks (to bridge macOS /tmp →
// /private/tmp aliasing), falling back to Clean on failure. Without this,
// the DB stores "/tmp/foo" while peercred.ResolvedIdentity.Worktree
// carries "/private/tmp/foo" and the equality check would fail.
func canonWorktreePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}
