package groups

import (
	"database/sql"
	"fmt"
)

// Resolver provides group existence checks and membership resolution.
type Resolver struct {
	db *sql.DB
}

// NewResolver creates a new group resolver.
func NewResolver(db *sql.DB) *Resolver {
	return &Resolver{db: db}
}

// IsGroup checks if a name corresponds to an existing group.
// Used at send-time to decide: group scope vs regular mention ref.
func (r *Resolver) IsGroup(name string) (bool, error) {
	var exists bool
	err := r.db.QueryRow("SELECT EXISTS(SELECT 1 FROM groups WHERE name = ?)", name).Scan(&exists)
	return exists, err
}

// IsMember checks if an agent belongs to a group (resolving roles).
func (r *Resolver) IsMember(groupName, agentID, agentRole string) (bool, error) {
	members, err := r.ExpandMembers(groupName)
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
func (r *Resolver) ExpandMembers(groupName string) ([]string, error) {
	rows, err := r.db.Query(`
		SELECT gm.member_type, gm.member_value
		FROM group_members gm
		JOIN groups g ON gm.group_id = g.group_id
		WHERE g.name = ?
	`, groupName)
	if err != nil {
		return nil, fmt.Errorf("query group members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var agents []string
	seen := make(map[string]bool)

	for rows.Next() {
		var memberType, memberValue string
		if err := rows.Scan(&memberType, &memberValue); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}

		switch memberType {
		case "agent":
			if !seen[memberValue] {
				agents = append(agents, memberValue)
				seen[memberValue] = true
			}
		case "role":
			var roleAgents []string
			if memberValue == "*" {
				roleAgents, err = r.queryAllAgents()
			} else {
				roleAgents, err = r.queryAgentsByRole(memberValue)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate members: %w", err)
	}

	return agents, nil
}

func (r *Resolver) queryAgentsByRole(role string) ([]string, error) {
	rows, err := r.db.Query("SELECT DISTINCT agent_id FROM agents WHERE role = ?", role)
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

func (r *Resolver) queryAllAgents() ([]string, error) {
	rows, err := r.db.Query("SELECT DISTINCT agent_id FROM agents")
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
