package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

// setupGroupTest creates a state with a registered agent and active session.
func setupGroupTest(t *testing.T) (*GroupHandler, *state.State, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}

	repoID := "r_GROUP_TEST"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")

	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerParams, _ := json.Marshal(RegisterRequest{Role: "tester", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("start session: %v", err)
	}

	handler := NewGroupHandler(st)
	return handler, st, func() { _ = st.Close() }
}

func TestGroupCreate(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

	req, _ := json.Marshal(GroupCreateRequest{
		Name:        "reviewers",
		Description: "Code reviewers",
	})

	resp, err := handler.HandleCreate(context.Background(), req)
	if err != nil {
		t.Fatalf("HandleCreate: %v", err)
	}

	createResp, ok := resp.(*GroupCreateResponse)
	if !ok {
		t.Fatalf("expected *GroupCreateResponse, got %T", resp)
	}
	if createResp.Name != "reviewers" {
		t.Errorf("expected name 'reviewers', got %q", createResp.Name)
	}
	if createResp.GroupID == "" {
		t.Error("expected non-empty group_id")
	}
}

func TestGroupCreate_DuplicateName(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

	req, _ := json.Marshal(GroupCreateRequest{Name: "reviewers"})
	if _, err := handler.HandleCreate(context.Background(), req); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := handler.HandleCreate(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestGroupDelete(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

	// Create then delete
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "temp"})
	if _, err := handler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create: %v", err)
	}

	deleteReq, _ := json.Marshal(GroupDeleteRequest{Name: "temp"})
	resp, err := handler.HandleDelete(context.Background(), deleteReq)
	if err != nil {
		t.Fatalf("HandleDelete: %v", err)
	}

	deleteResp, ok := resp.(*GroupDeleteResponse)
	if !ok {
		t.Fatalf("expected *GroupDeleteResponse, got %T", resp)
	}
	if deleteResp.Name != "temp" {
		t.Errorf("expected name 'temp', got %q", deleteResp.Name)
	}
}

func TestGroupDelete_ProtectedEveryone(t *testing.T) {
	handler, st, cleanup := setupGroupTest(t)
	defer cleanup()

	// Create @everyone
	if err := EnsureEveryoneGroup(context.Background(), st); err != nil {
		t.Fatalf("ensure everyone: %v", err)
	}

	deleteReq, _ := json.Marshal(GroupDeleteRequest{Name: "everyone"})
	_, err := handler.HandleDelete(context.Background(), deleteReq)
	if err == nil {
		t.Fatal("expected error when deleting @everyone")
	}
}

// registerTestAgent registers an agent with the given name in the test state.
func registerTestAgent(t *testing.T, st *state.State, name string) {
	t.Helper()
	agentHandler := NewAgentHandler(st)
	params, _ := json.Marshal(RegisterRequest{Name: name, Role: name + "_role", Module: name + "_mod", Force: true})
	if _, err := agentHandler.HandleRegister(context.Background(), params); err != nil {
		t.Fatalf("register agent %q: %v", name, err)
	}
}

func TestGroupMemberAdd(t *testing.T) {
	handler, st, cleanup := setupGroupTest(t)
	defer cleanup()

	registerTestAgent(t, st, "alice")

	// Create group first
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers"})
	if _, err := handler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create: %v", err)
	}

	addReq, _ := json.Marshal(GroupMemberAddRequest{
		Group:       "reviewers",
		MemberType:  "agent",
		MemberValue: "alice",
	})
	resp, err := handler.HandleMemberAdd(context.Background(), addReq)
	if err != nil {
		t.Fatalf("HandleMemberAdd: %v", err)
	}

	addResp, ok := resp.(*GroupMemberAddResponse)
	if !ok {
		t.Fatalf("expected *GroupMemberAddResponse, got %T", resp)
	}
	if addResp.MemberValue != "alice" {
		t.Errorf("expected member_value 'alice', got %q", addResp.MemberValue)
	}
}

func TestGroupMemberAdd_InvalidType(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers"})
	if _, err := handler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create: %v", err)
	}

	addReq, _ := json.Marshal(GroupMemberAddRequest{
		Group:       "reviewers",
		MemberType:  "invalid",
		MemberValue: "alice",
	})
	_, err := handler.HandleMemberAdd(context.Background(), addReq)
	if err == nil {
		t.Fatal("expected error for invalid member_type")
	}
}

func TestGroupMemberAdd_ProtectedEveryone(t *testing.T) {
	handler, st, cleanup := setupGroupTest(t)
	defer cleanup()

	if err := EnsureEveryoneGroup(context.Background(), st); err != nil {
		t.Fatalf("ensure everyone: %v", err)
	}

	addReq, _ := json.Marshal(GroupMemberAddRequest{
		Group:       "everyone",
		MemberType:  "agent",
		MemberValue: "alice",
	})
	_, err := handler.HandleMemberAdd(context.Background(), addReq)
	if err == nil {
		t.Fatal("expected error when adding to @everyone")
	}
}

func TestGroupMemberAdd_NonExistentAgent(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers"})
	if _, err := handler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create: %v", err)
	}

	addReq, _ := json.Marshal(GroupMemberAddRequest{
		Group:       "reviewers",
		MemberType:  "agent",
		MemberValue: "nonexistent",
	})
	_, err := handler.HandleMemberAdd(context.Background(), addReq)
	if err == nil {
		t.Fatal("expected error when adding non-existent agent to group")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestGroupMemberAdd_NonExistentRole(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers"})
	if _, err := handler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create: %v", err)
	}

	addReq, _ := json.Marshal(GroupMemberAddRequest{
		Group:       "reviewers",
		MemberType:  "role",
		MemberValue: "nonexistent_role",
	})
	_, err := handler.HandleMemberAdd(context.Background(), addReq)
	if err == nil {
		t.Fatal("expected error when adding non-existent role to group")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestGroupMemberRemove(t *testing.T) {
	handler, st, cleanup := setupGroupTest(t)
	defer cleanup()

	registerTestAgent(t, st, "alice")

	// Create group and add member
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers"})
	if _, err := handler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create: %v", err)
	}
	addReq, _ := json.Marshal(GroupMemberAddRequest{Group: "reviewers", MemberType: "agent", MemberValue: "alice"})
	if _, err := handler.HandleMemberAdd(context.Background(), addReq); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// Remove
	removeReq, _ := json.Marshal(GroupMemberRemoveRequest{Group: "reviewers", MemberType: "agent", MemberValue: "alice"})
	_, err := handler.HandleMemberRemove(context.Background(), removeReq)
	if err != nil {
		t.Fatalf("HandleMemberRemove: %v", err)
	}
}

func TestGroupList(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

	// Create two groups
	for _, name := range []string{"alpha", "beta"} {
		req, _ := json.Marshal(GroupCreateRequest{Name: name})
		if _, err := handler.HandleCreate(context.Background(), req); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	resp, err := handler.HandleList(context.Background(), nil)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}

	listResp, ok := resp.(*GroupListResponse)
	if !ok {
		t.Fatalf("expected *GroupListResponse, got %T", resp)
	}
	// setupGroupTest registers an agent with role "tester", which auto-creates a "tester" group.
	// Combined with the two manually created groups, we expect at least 3 groups.
	if len(listResp.Groups) < 2 {
		t.Errorf("expected at least 2 groups (alpha, beta), got %d", len(listResp.Groups))
	}
	// Verify alpha and beta are in the list
	groupNames := make(map[string]bool)
	for _, g := range listResp.Groups {
		groupNames[g.Name] = true
	}
	for _, want := range []string{"alpha", "beta"} {
		if !groupNames[want] {
			t.Errorf("expected group %q in list, not found", want)
		}
	}
}

func TestGroupInfo(t *testing.T) {
	handler, st, cleanup := setupGroupTest(t)
	defer cleanup()

	registerTestAgent(t, st, "alice")

	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers", Description: "Code reviewers"})
	if _, err := handler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Add a member
	addReq, _ := json.Marshal(GroupMemberAddRequest{Group: "reviewers", MemberType: "agent", MemberValue: "alice"})
	if _, err := handler.HandleMemberAdd(context.Background(), addReq); err != nil {
		t.Fatalf("add member: %v", err)
	}

	infoReq, _ := json.Marshal(GroupInfoRequest{Name: "reviewers"})
	resp, err := handler.HandleInfo(context.Background(), infoReq)
	if err != nil {
		t.Fatalf("HandleInfo: %v", err)
	}

	infoResp, ok := resp.(*GroupInfoResponse)
	if !ok {
		t.Fatalf("expected *GroupInfoResponse, got %T", resp)
	}
	if infoResp.Name != "reviewers" {
		t.Errorf("expected name 'reviewers', got %q", infoResp.Name)
	}
	if infoResp.Description != "Code reviewers" {
		t.Errorf("expected description 'Code reviewers', got %q", infoResp.Description)
	}
	if len(infoResp.Members) != 1 {
		t.Errorf("expected 1 member, got %d", len(infoResp.Members))
	}
}

func TestGroupMembers_WithExpand(t *testing.T) {
	handler, st, cleanup := setupGroupTest(t)
	defer cleanup()

	registerTestAgent(t, st, "alice")

	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers"})
	if _, err := handler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create: %v", err)
	}
	addReq, _ := json.Marshal(GroupMemberAddRequest{Group: "reviewers", MemberType: "agent", MemberValue: "alice"})
	if _, err := handler.HandleMemberAdd(context.Background(), addReq); err != nil {
		t.Fatalf("add member: %v", err)
	}

	membersReq, _ := json.Marshal(GroupMembersRequest{Name: "reviewers", Expand: true})
	resp, err := handler.HandleMembers(context.Background(), membersReq)
	if err != nil {
		t.Fatalf("HandleMembers: %v", err)
	}

	membersResp, ok := resp.(*GroupMembersResponse)
	if !ok {
		t.Fatalf("expected *GroupMembersResponse, got %T", resp)
	}
	if len(membersResp.Members) != 1 {
		t.Errorf("expected 1 raw member, got %d", len(membersResp.Members))
	}
	if len(membersResp.Expanded) != 1 || membersResp.Expanded[0] != "alice" {
		t.Errorf("expected expanded=[alice], got %v", membersResp.Expanded)
	}
}

func TestEnsureEveryoneGroup(t *testing.T) {
	_, st, cleanup := setupGroupTest(t)
	defer cleanup()

	// First call creates the group
	if err := EnsureEveryoneGroup(context.Background(), st); err != nil {
		t.Fatalf("first EnsureEveryoneGroup: %v", err)
	}

	// Verify group exists
	var name string
	err := st.RawDB().QueryRow("SELECT name FROM groups WHERE name = 'everyone'").Scan(&name)
	if err != nil {
		t.Fatalf("everyone group not created: %v", err)
	}

	// Verify role:* member
	var memberType, memberValue string
	err = st.RawDB().QueryRow(
		"SELECT member_type, member_value FROM group_members WHERE group_id = 'grp_everyone'",
	).Scan(&memberType, &memberValue)
	if err != nil {
		t.Fatalf("everyone member not created: %v", err)
	}
	if memberType != "role" || memberValue != "*" {
		t.Errorf("expected role:*, got %s:%s", memberType, memberValue)
	}

	// Second call is idempotent
	if err := EnsureEveryoneGroup(context.Background(), st); err != nil {
		t.Fatalf("second EnsureEveryoneGroup: %v", err)
	}

	// Should still be exactly 1 group named everyone
	var count int
	err = st.RawDB().QueryRow("SELECT COUNT(*) FROM groups WHERE name = 'everyone'").Scan(&count)
	if err != nil {
		t.Fatalf("count everyone: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 everyone group, got %d", count)
	}
}

func TestGroupDelete_NonExistent(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

	deleteReq, _ := json.Marshal(GroupDeleteRequest{Name: "nonexistent"})
	_, err := handler.HandleDelete(context.Background(), deleteReq)
	if err == nil {
		t.Fatal("expected error for non-existent group")
	}
}

// setupGroupTestWithMessages creates state with both a GroupHandler and a MessageHandler.
func setupGroupTestWithMessages(t *testing.T) (*GroupHandler, *MessageHandler, *state.State, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}

	repoID := "r_GROUP_MSG_TEST"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")

	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerParams, _ := json.Marshal(RegisterRequest{Role: "tester", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("start session: %v", err)
	}

	groupHandler := NewGroupHandler(st)
	msgHandler := NewMessageHandler(st)
	return groupHandler, msgHandler, st, func() { _ = st.Close() }
}

func TestGroupDelete_WithDeleteMessages_True(t *testing.T) {
	groupHandler, msgHandler, st, cleanup := setupGroupTestWithMessages(t)
	defer cleanup()

	ctx := context.Background()

	// Create a group
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "engineering"})
	if _, err := groupHandler.HandleCreate(ctx, createReq); err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Send two messages scoped to the group
	for i := 0; i < 2; i++ {
		sendParams, _ := json.Marshal(SendRequest{
			Content: "Engineering message",
			Scopes: []types.Scope{
				{Type: "group", Value: "engineering"},
			},
		})
		if _, err := msgHandler.HandleSend(ctx, sendParams); err != nil {
			t.Fatalf("send message %d: %v", i, err)
		}
	}

	// Verify messages are present before delete
	var countBefore int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM message_scopes WHERE scope_type = ? AND scope_value = ?",
		"group", "engineering",
	).Scan(&countBefore); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if countBefore != 2 {
		t.Fatalf("expected 2 scoped messages before delete, got %d", countBefore)
	}

	// Delete group with delete_messages=true
	deleteReq, _ := json.Marshal(GroupDeleteRequest{
		Name:           "engineering",
		DeleteMessages: true,
	})
	resp, err := groupHandler.HandleDelete(ctx, deleteReq)
	if err != nil {
		t.Fatalf("HandleDelete: %v", err)
	}
	deleteResp, ok := resp.(*GroupDeleteResponse)
	if !ok {
		t.Fatalf("expected *GroupDeleteResponse, got %T", resp)
	}
	if deleteResp.Name != "engineering" {
		t.Errorf("expected name 'engineering', got %q", deleteResp.Name)
	}

	// Verify group is gone
	var groupCount int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM groups WHERE name = ?", "engineering",
	).Scan(&groupCount); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if groupCount != 0 {
		t.Errorf("expected group to be deleted, but found %d rows", groupCount)
	}

	// Verify messages are gone
	var msgCount int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM message_scopes WHERE scope_type = ? AND scope_value = ?",
		"group", "engineering",
	).Scan(&msgCount); err != nil {
		t.Fatalf("count message_scopes: %v", err)
	}
	if msgCount != 0 {
		t.Errorf("expected 0 message scopes after delete, got %d", msgCount)
	}

	var msgRowCount int
	if err := st.RawDB().QueryRow("SELECT COUNT(*) FROM messages WHERE deleted = 0").Scan(&msgRowCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgRowCount != 0 {
		t.Errorf("expected 0 messages after delete_messages=true, got %d", msgRowCount)
	}
}

func TestGroupDelete_WithDeleteMessages_False(t *testing.T) {
	groupHandler, msgHandler, st, cleanup := setupGroupTestWithMessages(t)
	defer cleanup()

	ctx := context.Background()

	// Create a group
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "design"})
	if _, err := groupHandler.HandleCreate(ctx, createReq); err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Send a message scoped to the group
	sendParams, _ := json.Marshal(SendRequest{
		Content: "Design message",
		Scopes: []types.Scope{
			{Type: "group", Value: "design"},
		},
	})
	if _, err := msgHandler.HandleSend(ctx, sendParams); err != nil {
		t.Fatalf("send message: %v", err)
	}

	// Delete group WITHOUT delete_messages (default false)
	deleteReq, _ := json.Marshal(GroupDeleteRequest{
		Name:           "design",
		DeleteMessages: false,
	})
	if _, err := groupHandler.HandleDelete(ctx, deleteReq); err != nil {
		t.Fatalf("HandleDelete: %v", err)
	}

	// Verify group is gone
	var groupCount int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM groups WHERE name = ?", "design",
	).Scan(&groupCount); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if groupCount != 0 {
		t.Errorf("expected group to be deleted, but found %d rows", groupCount)
	}

	// Verify messages are still present (delete_messages=false)
	var scopeCount int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM message_scopes WHERE scope_type = ? AND scope_value = ?",
		"group", "design",
	).Scan(&scopeCount); err != nil {
		t.Fatalf("count message_scopes: %v", err)
	}
	if scopeCount != 1 {
		t.Errorf("expected 1 message scope to remain (delete_messages=false), got %d", scopeCount)
	}
}
