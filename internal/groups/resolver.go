package groups

import (
	"context"
	"fmt"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// Resolver provides group existence checks and membership resolution.
type Resolver struct {
	db *safedb.DB
}

// NewResolver creates a new group resolver.
func NewResolver(db *safedb.DB) *Resolver {
	return &Resolver{db: db}
}

// IsGroup checks if a name corresponds to an existing group.
// Used at send-time to decide: group scope vs regular mention ref.
func (r *Resolver) IsGroup(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM groups WHERE name = ?)", name).Scan(&exists)
	return exists, err
}

// IsMember checks if an agent belongs to a group (resolving roles).
func (r *Resolver) IsMember(ctx context.Context, groupName, agentID, agentRole string) (bool, error) {
	members, err := r.ExpandMembers(ctx, groupName)
	if err != nil {
		return false, err
	}
	for _, m := range members {
		if m == agentID {
			return true, nil
		}
	}
	return false, nil
}

// ExpandMembers resolves a group to a deduplicated list of agent IDs.
// Handles agent and role members (flat groups only, no nesting).
func (r *Resolver) ExpandMembers(ctx context.Context, groupName string) ([]string, error) {
	// Collect all members first, then close the cursor before sub-queries.
	// SQLite with SetMaxOpenConns(1) deadlocks if we query inside an open rows cursor.
	type member struct {
		typ, value string
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT gm.member_type, gm.member_value
		FROM group_members gm
		JOIN groups g ON gm.group_id = g.group_id
		WHERE g.name = ?
	`, groupName)
	if err != nil {
		return nil, fmt.Errorf("query group members: %w", err)
	}

	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.typ, &m.value); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan member: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate members: %w", err)
	}
	_ = rows.Close()

	// Now resolve roles with the cursor closed.
	var agents []string
	seen := make(map[string]bool)

	for _, m := range members {
		switch m.typ {
		case "agent":
			if !seen[m.value] {
				agents = append(agents, m.value)
				seen[m.value] = true
			}
		case "role":
			var roleAgents []string
			if m.value == "*" {
				roleAgents, err = r.queryAllAgents(ctx)
			} else {
				roleAgents, err = r.queryAgentsByRole(ctx, m.value)
			}
			if err != nil {
				return nil, err
			}
			for _, a := range roleAgents {
				if !seen[a] {
					agents = append(agents, a)
					seen[a] = true
				}
			}
		}
	}

	return agents, nil
}

func (r *Resolver) queryAgentsByRole(ctx context.Context, role string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT DISTINCT agent_id FROM agents WHERE role = ?", role)
	if err != nil {
		return nil, fmt.Errorf("query agents by role: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var agents []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		agents = append(agents, id)
	}
	return agents, rows.Err()
}

func (r *Resolver) queryAllAgents(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT DISTINCT agent_id FROM agents")
	if err != nil {
		return nil, fmt.Errorf("query all agents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var agents []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		agents = append(agents, id)
	}
	return agents, rows.Err()
}
