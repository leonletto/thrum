package state

import (
	"context"
	"fmt"
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
