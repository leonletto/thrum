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

// IsMember checks if an agent belongs to a group (resolving roles and nested groups).
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
// Handles agent, role, and nested group members with cycle detection.
func (r *Resolver) ExpandMembers(groupName string) ([]string, error) {
	visited := make(map[string]bool)
	seen := make(map[string]bool)
	return r.expandWithVisited(groupName, visited, seen)
}

func (r *Resolver) expandWithVisited(groupName string, visited, seen map[string]bool) ([]string, error) {
	if visited[groupName] {
		return nil, nil // Cycle — skip silently
	}
	visited[groupName] = true

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
	// Collect all members first to close rows before recursive queries
	type member struct {
		memberType  string
		memberValue string
	}
	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.memberType, &m.memberValue); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate members: %w", err)
	}

	for _, m := range members {
		switch m.memberType {
		case "agent":
			if !seen[m.memberValue] {
				agents = append(agents, m.memberValue)
				seen[m.memberValue] = true
			}
		case "role":
			var roleAgents []string
			if m.memberValue == "*" {
				roleAgents, err = r.queryAllAgents()
			} else {
				roleAgents, err = r.queryAgentsByRole(m.memberValue)
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
		case "group":
			nested, err := r.expandWithVisited(m.memberValue, visited, seen)
			if err != nil {
				return nil, err
			}
			agents = append(agents, nested...)
		}
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
