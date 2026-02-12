package groups

import (
	"database/sql"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
)

// setupTestDB creates an in-memory SQLite database with the current schema.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// insertGroup inserts a group directly into the database.
func insertGroup(t *testing.T, db *sql.DB, groupID, name, description string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO groups (group_id, name, description, created_at, created_by) VALUES (?, ?, ?, '2026-01-01T00:00:00Z', 'test')`,
		groupID, name, description,
	)
	if err != nil {
		t.Fatalf("insert group %s: %v", name, err)
	}
}

// insertMember inserts a group member directly into the database.
func insertMember(t *testing.T, db *sql.DB, groupID, memberType, memberValue string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO group_members (group_id, member_type, member_value, added_at) VALUES (?, ?, ?, '2026-01-01T00:00:00Z')`,
		groupID, memberType, memberValue,
	)
	if err != nil {
		t.Fatalf("insert member %s/%s: %v", memberType, memberValue, err)
	}
}

// insertAgent inserts an agent directly into the database.
func insertAgent(t *testing.T, db *sql.DB, agentID, role string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO agents (agent_id, kind, role, module, registered_at) VALUES (?, 'agent', ?, 'test', '2026-01-01T00:00:00Z')`,
		agentID, role,
	)
	if err != nil {
		t.Fatalf("insert agent %s: %v", agentID, err)
	}
}

func TestIsGroup(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	insertGroup(t, db, "grp_1", "reviewers", "Code reviewers")

	ok, err := r.IsGroup("reviewers")
	if err != nil {
		t.Fatalf("IsGroup: %v", err)
	}
	if !ok {
		t.Error("expected IsGroup('reviewers') = true")
	}

	ok, err = r.IsGroup("nonexistent")
	if err != nil {
		t.Fatalf("IsGroup: %v", err)
	}
	if ok {
		t.Error("expected IsGroup('nonexistent') = false")
	}
}

func TestExpandMembers_AgentOnly(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	insertGroup(t, db, "grp_1", "reviewers", "")
	insertMember(t, db, "grp_1", "agent", "alice")
	insertMember(t, db, "grp_1", "agent", "bob")

	members, err := r.ExpandMembers("reviewers")
	if err != nil {
		t.Fatalf("ExpandMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d: %v", len(members), members)
	}

	found := map[string]bool{}
	for _, m := range members {
		found[m] = true
	}
	if !found["alice"] || !found["bob"] {
		t.Errorf("expected alice and bob, got %v", members)
	}
}

func TestExpandMembers_RoleMembers(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	insertAgent(t, db, "agent:charlie:1", "reviewer")
	insertAgent(t, db, "agent:dana:1", "reviewer")
	insertAgent(t, db, "agent:eve:1", "deployer")

	insertGroup(t, db, "grp_1", "reviewers", "")
	insertMember(t, db, "grp_1", "role", "reviewer")

	members, err := r.ExpandMembers("reviewers")
	if err != nil {
		t.Fatalf("ExpandMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d: %v", len(members), members)
	}

	found := map[string]bool{}
	for _, m := range members {
		found[m] = true
	}
	if !found["agent:charlie:1"] || !found["agent:dana:1"] {
		t.Errorf("expected charlie and dana, got %v", members)
	}
}

func TestExpandMembers_NestedGroups(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	insertGroup(t, db, "grp_1", "inner", "")
	insertMember(t, db, "grp_1", "agent", "alice")

	insertGroup(t, db, "grp_2", "outer", "")
	insertMember(t, db, "grp_2", "agent", "bob")
	insertMember(t, db, "grp_2", "group", "inner")

	members, err := r.ExpandMembers("outer")
	if err != nil {
		t.Fatalf("ExpandMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d: %v", len(members), members)
	}

	found := map[string]bool{}
	for _, m := range members {
		found[m] = true
	}
	if !found["alice"] || !found["bob"] {
		t.Errorf("expected alice and bob, got %v", members)
	}
}

func TestExpandMembers_WildcardRole(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	insertAgent(t, db, "agent:alice:1", "reviewer")
	insertAgent(t, db, "agent:bob:1", "deployer")

	insertGroup(t, db, "grp_everyone", "everyone", "All agents")
	insertMember(t, db, "grp_everyone", "role", "*")

	members, err := r.ExpandMembers("everyone")
	if err != nil {
		t.Fatalf("ExpandMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d: %v", len(members), members)
	}
}

func TestExpandMembers_CycleDetection(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	insertGroup(t, db, "grp_a", "group-a", "")
	insertGroup(t, db, "grp_b", "group-b", "")
	insertMember(t, db, "grp_a", "agent", "alice")
	insertMember(t, db, "grp_a", "group", "group-b")
	insertMember(t, db, "grp_b", "agent", "bob")
	insertMember(t, db, "grp_b", "group", "group-a") // Cycle: A→B→A

	members, err := r.ExpandMembers("group-a")
	if err != nil {
		t.Fatalf("ExpandMembers with cycle: %v", err)
	}

	found := map[string]bool{}
	for _, m := range members {
		found[m] = true
	}
	if !found["alice"] || !found["bob"] {
		t.Errorf("expected alice and bob despite cycle, got %v", members)
	}
	if len(members) != 2 {
		t.Errorf("expected 2 unique members (deduped), got %d: %v", len(members), members)
	}
}

func TestExpandMembers_EmptyGroup(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	insertGroup(t, db, "grp_empty", "empty", "")

	members, err := r.ExpandMembers("empty")
	if err != nil {
		t.Fatalf("ExpandMembers: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected 0 members, got %d: %v", len(members), members)
	}
}

func TestExpandMembers_NonExistentGroup(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	members, err := r.ExpandMembers("nonexistent")
	if err != nil {
		t.Fatalf("ExpandMembers: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected 0 members for non-existent group, got %d", len(members))
	}
}

func TestExpandMembers_Deduplication(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	// alice is in both inner groups
	insertGroup(t, db, "grp_1", "team-a", "")
	insertMember(t, db, "grp_1", "agent", "alice")

	insertGroup(t, db, "grp_2", "team-b", "")
	insertMember(t, db, "grp_2", "agent", "alice")

	insertGroup(t, db, "grp_3", "all-teams", "")
	insertMember(t, db, "grp_3", "group", "team-a")
	insertMember(t, db, "grp_3", "group", "team-b")

	members, err := r.ExpandMembers("all-teams")
	if err != nil {
		t.Fatalf("ExpandMembers: %v", err)
	}
	if len(members) != 1 {
		t.Errorf("expected 1 unique member (deduped), got %d: %v", len(members), members)
	}
	if len(members) > 0 && members[0] != "alice" {
		t.Errorf("expected alice, got %v", members)
	}
}

func TestIsMember(t *testing.T) {
	db := setupTestDB(t)
	r := NewResolver(db)

	insertAgent(t, db, "agent:alice:1", "reviewer")
	insertGroup(t, db, "grp_1", "reviewers", "")
	insertMember(t, db, "grp_1", "agent", "agent:alice:1")

	ok, err := r.IsMember("reviewers", "agent:alice:1", "reviewer")
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
	if !ok {
		t.Error("expected alice to be a member")
	}

	ok, err = r.IsMember("reviewers", "agent:bob:1", "deployer")
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
	if ok {
		t.Error("expected bob NOT to be a member")
	}
}
