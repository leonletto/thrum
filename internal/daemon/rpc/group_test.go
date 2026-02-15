package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
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

func TestGroupMemberAdd(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

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

func TestGroupMemberRemove(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

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
	if len(listResp.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(listResp.Groups))
	}
}

func TestGroupInfo(t *testing.T) {
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

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
	handler, _, cleanup := setupGroupTest(t)
	defer cleanup()

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
	err := st.DB().QueryRow("SELECT name FROM groups WHERE name = 'everyone'").Scan(&name)
	if err != nil {
		t.Fatalf("everyone group not created: %v", err)
	}

	// Verify role:* member
	var memberType, memberValue string
	err = st.DB().QueryRow(
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
	err = st.DB().QueryRow("SELECT COUNT(*) FROM groups WHERE name = 'everyone'").Scan(&count)
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
