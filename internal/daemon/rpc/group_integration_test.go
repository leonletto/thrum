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

// setupGroupIntegrationTest creates state with two agents (alice, bob) and active sessions.
// Returns handlers, state, agent IDs, and a cleanup function.
func setupGroupIntegrationTest(t *testing.T) (
	groupH *GroupHandler,
	msgH *MessageHandler,
	st *state.State,
	aliceID string,
	bobID string,
	cleanup func(),
) {
	t.Helper()

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}

	repoID := "r_GROUP_INTEG"
	var err error
	st, err = state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	agentHandler := NewAgentHandler(st)
	sessionHandler := NewSessionHandler(st)

	// Register alice
	aliceID = identity.GenerateAgentID(repoID, "reviewer", "review-mod", "alice")
	registerAlice, _ := json.Marshal(RegisterRequest{Name: "alice", Role: "reviewer", Module: "review-mod", Display: "alice"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerAlice); err != nil {
		t.Fatalf("register alice: %v", err)
	}
	sessionAlice, _ := json.Marshal(SessionStartRequest{AgentID: aliceID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionAlice); err != nil {
		t.Fatalf("start alice session: %v", err)
	}

	// Register bob
	bobID = identity.GenerateAgentID(repoID, "deployer", "deploy-mod", "bob")
	registerBob, _ := json.Marshal(RegisterRequest{Name: "bob", Role: "deployer", Module: "deploy-mod", Display: "bob"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerBob); err != nil {
		t.Fatalf("register bob: %v", err)
	}
	sessionBob, _ := json.Marshal(SessionStartRequest{AgentID: bobID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionBob); err != nil {
		t.Fatalf("start bob session: %v", err)
	}

	// Ensure @everyone group
	if err := EnsureEveryoneGroup(st); err != nil {
		t.Fatalf("ensure everyone: %v", err)
	}

	groupH = NewGroupHandler(st)
	msgH = NewMessageHandler(st)

	return groupH, msgH, st, aliceID, bobID, func() { _ = st.Close() }
}

// sendMessage is a helper that sends a message with given mentions and returns the message ID.
func sendMessage(t *testing.T, handler *MessageHandler, content string, mentions []string, callerAgentID string) string {
	t.Helper()
	req := SendRequest{
		Content:       content,
		Format:        "markdown",
		Mentions:      mentions,
		CallerAgentID: callerAgentID,
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleSend(context.Background(), params)
	if err != nil {
		t.Fatalf("send message %q: %v", content, err)
	}
	return resp.(*SendResponse).MessageID
}

// listInbox returns message IDs visible to the given agent.
func listInbox(t *testing.T, handler *MessageHandler, agentID, agentRole string) []string {
	t.Helper()
	req := ListMessagesRequest{
		ForAgent:     agentID,
		ForAgentRole: agentRole,
		PageSize:     100,
		SortBy:       "created_at",
		SortOrder:    "asc",
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleList(context.Background(), params)
	if err != nil {
		t.Fatalf("list inbox for %s: %v", agentID, err)
	}
	listResp := resp.(*ListMessagesResponse)
	ids := make([]string, len(listResp.Messages))
	for i, m := range listResp.Messages {
		ids[i] = m.MessageID
	}
	return ids
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func TestGroupIntegration_SendToGroup_MembersReceive(t *testing.T) {
	groupH, msgH, _, aliceID, bobID, cleanup := setupGroupIntegrationTest(t)
	defer cleanup()

	// Create group and add alice
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers", Description: "Code reviewers"})
	if _, err := groupH.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create group: %v", err)
	}
	addReq, _ := json.Marshal(GroupMemberAddRequest{Group: "reviewers", MemberType: "agent", MemberValue: "alice"})
	if _, err := groupH.HandleMemberAdd(context.Background(), addReq); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// Send to @reviewers
	msgID := sendMessage(t, msgH, "Review this PR", []string{"@reviewers"}, bobID)

	// Alice should see it (member of reviewers)
	aliceInbox := listInbox(t, msgH, aliceID, "reviewer")
	if !containsID(aliceInbox, msgID) {
		t.Errorf("alice should see message sent to @reviewers, inbox: %v", aliceInbox)
	}

	// Bob should NOT see it (not a member of reviewers, unless there's a broadcast fallback)
	bobInbox := listInbox(t, msgH, bobID, "deployer")
	if containsID(bobInbox, msgID) {
		t.Errorf("bob should NOT see message sent to @reviewers (not a member)")
	}
}

func TestGroupIntegration_SendToEveryone_AllReceive(t *testing.T) {
	_, msgH, _, aliceID, bobID, cleanup := setupGroupIntegrationTest(t)
	defer cleanup()

	// Send to @everyone
	msgID := sendMessage(t, msgH, "Standup in 5 minutes", []string{"@everyone"}, aliceID)

	// Both alice and bob should see it (everyone has role:* wildcard)
	aliceInbox := listInbox(t, msgH, aliceID, "reviewer")
	if !containsID(aliceInbox, msgID) {
		t.Errorf("alice should see @everyone message, inbox: %v", aliceInbox)
	}

	bobInbox := listInbox(t, msgH, bobID, "deployer")
	if !containsID(bobInbox, msgID) {
		t.Errorf("bob should see @everyone message, inbox: %v", bobInbox)
	}
}

func TestGroupIntegration_GroupScope_NotMentionRef(t *testing.T) {
	groupH, msgH, st, _, bobID, cleanup := setupGroupIntegrationTest(t)
	defer cleanup()

	// Create group
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers"})
	if _, err := groupH.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Send to @reviewers
	msgID := sendMessage(t, msgH, "Group message", []string{"@reviewers"}, bobID)

	// Verify: should have group scope, NOT mention ref
	var scopeCount int
	err := st.DB().QueryRow(
		"SELECT COUNT(*) FROM message_scopes WHERE message_id = ? AND scope_type = 'group' AND scope_value = 'reviewers'",
		msgID,
	).Scan(&scopeCount)
	if err != nil {
		t.Fatalf("query scopes: %v", err)
	}
	if scopeCount != 1 {
		t.Errorf("expected 1 group scope, got %d", scopeCount)
	}

	// Should have group audit ref, NOT mention ref
	var groupRefCount, mentionRefCount int
	err = st.DB().QueryRow(
		"SELECT COUNT(*) FROM message_refs WHERE message_id = ? AND ref_type = 'group' AND ref_value = 'reviewers'",
		msgID,
	).Scan(&groupRefCount)
	if err != nil {
		t.Fatalf("query group refs: %v", err)
	}
	if groupRefCount != 1 {
		t.Errorf("expected 1 group audit ref, got %d", groupRefCount)
	}

	err = st.DB().QueryRow(
		"SELECT COUNT(*) FROM message_refs WHERE message_id = ? AND ref_type = 'mention' AND ref_value = 'reviewers'",
		msgID,
	).Scan(&mentionRefCount)
	if err != nil {
		t.Fatalf("query mention refs: %v", err)
	}
	if mentionRefCount != 0 {
		t.Errorf("should NOT have mention ref for group, got %d", mentionRefCount)
	}
}

func TestGroupIntegration_NonGroupMention_FallsThrough(t *testing.T) {
	_, msgH, st, _, bobID, cleanup := setupGroupIntegrationTest(t)
	defer cleanup()

	// Send to @charlie (not a group, should fall through to mention ref)
	msgID := sendMessage(t, msgH, "Direct message", []string{"@charlie"}, bobID)

	// Should have mention ref, NOT group scope
	var mentionCount int
	err := st.DB().QueryRow(
		"SELECT COUNT(*) FROM message_refs WHERE message_id = ? AND ref_type = 'mention' AND ref_value = 'charlie'",
		msgID,
	).Scan(&mentionCount)
	if err != nil {
		t.Fatalf("query mention refs: %v", err)
	}
	if mentionCount != 1 {
		t.Errorf("expected 1 mention ref for charlie, got %d", mentionCount)
	}

	var scopeCount int
	err = st.DB().QueryRow(
		"SELECT COUNT(*) FROM message_scopes WHERE message_id = ? AND scope_type = 'group'",
		msgID,
	).Scan(&scopeCount)
	if err != nil {
		t.Fatalf("query group scopes: %v", err)
	}
	if scopeCount != 0 {
		t.Errorf("should NOT have group scope for non-group mention, got %d", scopeCount)
	}
}

func TestGroupIntegration_DirectMention_StillWorks(t *testing.T) {
	_, msgH, _, aliceID, bobID, cleanup := setupGroupIntegrationTest(t)
	defer cleanup()

	// Send direct mention to alice's role
	msgID := sendMessage(t, msgH, "Hey alice", []string{"@reviewer"}, bobID)

	// Alice should see it via direct mention
	aliceInbox := listInbox(t, msgH, aliceID, "reviewer")
	if !containsID(aliceInbox, msgID) {
		t.Errorf("alice should see direct mention, inbox: %v", aliceInbox)
	}
}

func TestGroupIntegration_NestedGroupRejected(t *testing.T) {
	groupH, _, _, _, _, cleanup := setupGroupIntegrationTest(t)
	defer cleanup()

	// Create two groups
	createA, _ := json.Marshal(GroupCreateRequest{Name: "watchers"})
	if _, err := groupH.HandleCreate(context.Background(), createA); err != nil {
		t.Fatalf("create watchers: %v", err)
	}
	createB, _ := json.Marshal(GroupCreateRequest{Name: "reviewers"})
	if _, err := groupH.HandleCreate(context.Background(), createB); err != nil {
		t.Fatalf("create reviewers: %v", err)
	}

	// Adding a group as member should be rejected (flat groups only)
	addNested, _ := json.Marshal(GroupMemberAddRequest{Group: "watchers", MemberType: "group", MemberValue: "reviewers"})
	_, err := groupH.HandleMemberAdd(context.Background(), addNested)
	if err == nil {
		t.Fatal("expected error when adding group as member, got nil")
	}
}

func TestGroupIntegration_RoleBasedMember(t *testing.T) {
	groupH, msgH, _, aliceID, bobID, cleanup := setupGroupIntegrationTest(t)
	defer cleanup()

	// Create group with role:reviewer member
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "code-watchers"})
	if _, err := groupH.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create group: %v", err)
	}
	addRole, _ := json.Marshal(GroupMemberAddRequest{Group: "code-watchers", MemberType: "role", MemberValue: "reviewer"})
	if _, err := groupH.HandleMemberAdd(context.Background(), addRole); err != nil {
		t.Fatalf("add role member: %v", err)
	}

	// Send to @code-watchers
	msgID := sendMessage(t, msgH, "Code update", []string{"@code-watchers"}, bobID)

	// Alice (role=reviewer) should see it
	aliceInbox := listInbox(t, msgH, aliceID, "reviewer")
	if !containsID(aliceInbox, msgID) {
		t.Errorf("alice (reviewer) should see message to @code-watchers, inbox: %v", aliceInbox)
	}

	// Bob (role=deployer) should NOT see it
	bobInbox := listInbox(t, msgH, bobID, "deployer")
	if containsID(bobInbox, msgID) {
		t.Errorf("bob (deployer) should NOT see message to @code-watchers")
	}
}

func TestGroupIntegration_GroupCRUD(t *testing.T) {
	groupH, _, _, _, _, cleanup := setupGroupIntegrationTest(t)
	defer cleanup()

	// Create
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "devops", Description: "DevOps team"})
	resp, err := groupH.HandleCreate(context.Background(), createReq)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	createResp := resp.(*GroupCreateResponse)
	if createResp.Name != "devops" {
		t.Errorf("expected name devops, got %q", createResp.Name)
	}

	// Info
	infoReq, _ := json.Marshal(GroupInfoRequest{Name: "devops"})
	infoResp, err := groupH.HandleInfo(context.Background(), infoReq)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	info := infoResp.(*GroupInfoResponse)
	if info.Description != "DevOps team" {
		t.Errorf("expected description 'DevOps team', got %q", info.Description)
	}

	// Add member
	addReq, _ := json.Marshal(GroupMemberAddRequest{Group: "devops", MemberType: "agent", MemberValue: "alice"})
	if _, err := groupH.HandleMemberAdd(context.Background(), addReq); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// Members
	membersReq, _ := json.Marshal(GroupMembersRequest{Name: "devops"})
	membersResp, err := groupH.HandleMembers(context.Background(), membersReq)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	members := membersResp.(*GroupMembersResponse)
	if len(members.Members) != 1 {
		t.Errorf("expected 1 member, got %d", len(members.Members))
	}

	// Remove member
	removeReq, _ := json.Marshal(GroupMemberRemoveRequest{Group: "devops", MemberType: "agent", MemberValue: "alice"})
	if _, err := groupH.HandleMemberRemove(context.Background(), removeReq); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	// Verify removed
	membersResp2, err := groupH.HandleMembers(context.Background(), membersReq)
	if err != nil {
		t.Fatalf("members after remove: %v", err)
	}
	members2 := membersResp2.(*GroupMembersResponse)
	if len(members2.Members) != 0 {
		t.Errorf("expected 0 members after remove, got %d", len(members2.Members))
	}

	// Delete
	deleteReq, _ := json.Marshal(GroupDeleteRequest{Name: "devops"})
	if _, err := groupH.HandleDelete(context.Background(), deleteReq); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Verify deleted
	_, err = groupH.HandleInfo(context.Background(), infoReq)
	if err == nil {
		t.Error("expected error for deleted group")
	}
}

func TestGroupIntegration_NonMembersExcluded(t *testing.T) {
	groupH, msgH, _, _, bobID, cleanup := setupGroupIntegrationTest(t)
	defer cleanup()

	// Create private group with no members
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "secret"})
	if _, err := groupH.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Send to @secret
	msgID := sendMessage(t, msgH, "Secret message", []string{"@secret"}, bobID)

	// Bob should NOT see it (not a member)
	bobInbox := listInbox(t, msgH, bobID, "deployer")
	if containsID(bobInbox, msgID) {
		t.Errorf("bob should NOT see message to @secret (not a member)")
	}
}
