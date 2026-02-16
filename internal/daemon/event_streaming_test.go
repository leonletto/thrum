package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/subscriptions"
	"github.com/leonletto/thrum/internal/types"
	"github.com/leonletto/thrum/internal/websocket"
)

// mockNotificationReceiver collects notifications for testing.
type mockNotificationReceiver struct {
	notifications []any
}

func (m *mockNotificationReceiver) Notify(sessionID string, notification any) error {
	m.notifications = append(m.notifications, notification)
	return nil
}

func TestEventStreamingIntegration(t *testing.T) {
	// Setup test environment
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Set up test identity via environment variables
	t.Setenv("THRUM_ROLE", "test")
	t.Setenv("THRUM_MODULE", "integration")

	// Create state
	st, err := state.NewState(thrumDir, thrumDir, "test-repo-123")
	if err != nil {
		t.Fatalf("Failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Register test agent
	agentHandler := rpc.NewAgentHandler(st)
	agentReq := rpc.RegisterRequest{
		Role:   "test",
		Module: "integration",
	}
	agentReqJSON, err := json.Marshal(agentReq)
	if err != nil {
		t.Fatalf("Failed to marshal agent request: %v", err)
	}

	agentResp, err := agentHandler.HandleRegister(context.Background(), agentReqJSON)
	if err != nil {
		t.Fatalf("Failed to register agent: %v", err)
	}

	registerResp, ok := agentResp.(*rpc.RegisterResponse)
	if !ok {
		t.Fatalf("Expected *rpc.RegisterResponse, got %T", agentResp)
	}
	agentID := registerResp.AgentID

	// Start session
	sessionHandler := rpc.NewSessionHandler(st)
	sessionReq := rpc.SessionStartRequest{
		AgentID: agentID,
	}
	sessionReqJSON, err := json.Marshal(sessionReq)
	if err != nil {
		t.Fatalf("Failed to marshal session request: %v", err)
	}

	sessionResp, err := sessionHandler.HandleStart(context.Background(), sessionReqJSON)
	if err != nil {
		t.Fatalf("Failed to start session: %v", err)
	}

	startResp, ok := sessionResp.(*rpc.SessionStartResponse)
	if !ok {
		t.Fatalf("Expected *rpc.SessionStartResponse, got %T", sessionResp)
	}
	sessionID := startResp.SessionID

	// Create subscription for all messages
	subService := subscriptions.NewService(st.DB())
	_, err = subService.Subscribe(context.Background(), sessionID, nil, nil, true)
	if err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	// Create mock notification receiver (simulates WebSocket client)
	receiver := &mockNotificationReceiver{}

	// Create dispatcher with the mock receiver
	dispatcher := subscriptions.NewDispatcher(st.DB())
	dispatcher.SetClientNotifier(receiver)

	// Create message handler with the dispatcher
	messageHandler := rpc.NewMessageHandlerWithDispatcher(st, dispatcher)

	// Send a message
	sendReq := rpc.SendRequest{
		Content: "Test message for event streaming",
		Scopes: []types.Scope{
			{Type: "task", Value: "thrum-ukr"},
		},
	}
	sendReqJSON, err := json.Marshal(sendReq)
	if err != nil {
		t.Fatalf("Failed to marshal send request: %v", err)
	}

	// Execute send in the session context
	ctx := context.Background()
	// TODO: Add session context when available
	_, err = messageHandler.HandleSend(ctx, sendReqJSON)
	if err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Verify notification was sent
	if len(receiver.notifications) != 1 {
		t.Fatalf("Expected 1 notification, got %d", len(receiver.notifications))
	}

	// Verify notification structure
	notification := receiver.notifications[0]
	notifMap, ok := notification.(map[string]any)
	if !ok {
		t.Fatalf("Notification is not a map: %T", notification)
	}

	if notifMap["method"] != "notification.message" {
		t.Errorf("Expected method 'notification.message', got %v", notifMap["method"])
	}

	params, ok := notifMap["params"].(map[string]any)
	if !ok {
		t.Fatalf("Params is not a map: %T", notifMap["params"])
	}

	if params["preview"] != "Test message for event streaming" {
		t.Errorf("Expected preview 'Test message for event streaming', got %v", params["preview"])
	}

	// Verify matched subscription info
	matched, ok := params["matched_subscription"].(map[string]any)
	if !ok {
		t.Fatalf("matched_subscription is not a map: %T", params["matched_subscription"])
	}

	if matched["match_type"] != "all" {
		t.Errorf("Expected match_type 'all', got %v", matched["match_type"])
	}
}

func TestEventStreamingSetup(t *testing.T) {
	// Setup test environment
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Set up test identity via environment variables
	t.Setenv("THRUM_ROLE", "test")
	t.Setenv("THRUM_MODULE", "integration")

	// Create state
	st, err := state.NewState(thrumDir, thrumDir, "test-repo-123")
	if err != nil {
		t.Fatalf("Failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Create mock client registries
	unixClients := NewClientRegistry()
	wsRegistry := websocket.NewSimpleRegistry()
	wsServer := websocket.NewServer("localhost:9999", wsRegistry, nil)

	// Create event streaming setup
	setup := NewEventStreamingSetupFromState(st, unixClients, wsServer)

	if setup == nil {
		t.Fatal("Expected non-nil setup")
	}

	if setup.Broadcaster == nil {
		t.Error("Expected non-nil broadcaster")
	}

	if setup.Dispatcher == nil {
		t.Error("Expected non-nil dispatcher")
	}
}

func TestEventStreamingWithSubscriptionFiltering(t *testing.T) {
	// Setup test environment
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Set up test identity via environment variables (for the sending agent)
	t.Setenv("THRUM_ROLE", "sender")
	t.Setenv("THRUM_MODULE", "test")

	// Create state
	st, err := state.NewState(thrumDir, thrumDir, "test-repo-123")
	if err != nil {
		t.Fatalf("Failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Register three agents: sender (for messages), subscriber1, subscriber2
	agentHandler := rpc.NewAgentHandler(st)

	// Sender agent
	senderReq := rpc.RegisterRequest{Role: "sender", Module: "test"}
	senderReqJSON, err := json.Marshal(senderReq)
	if err != nil {
		t.Fatalf("Failed to marshal sender request: %v", err)
	}
	senderResp, err := agentHandler.HandleRegister(context.Background(), senderReqJSON)
	if err != nil {
		t.Fatalf("Failed to register sender agent: %v", err)
	}
	senderRegResp, ok := senderResp.(*rpc.RegisterResponse)
	if !ok {
		t.Fatalf("Expected *rpc.RegisterResponse for sender, got %T", senderResp)
	}
	senderID := senderRegResp.AgentID

	// Subscriber 1
	sub1Req := rpc.RegisterRequest{Role: "subscriber1", Module: "test"}
	sub1ReqJSON, err := json.Marshal(sub1Req)
	if err != nil {
		t.Fatalf("Failed to marshal sub1 request: %v", err)
	}
	sub1Resp, err := agentHandler.HandleRegister(context.Background(), sub1ReqJSON)
	if err != nil {
		t.Fatalf("Failed to register subscriber1 agent: %v", err)
	}
	sub1RegResp, ok := sub1Resp.(*rpc.RegisterResponse)
	if !ok {
		t.Fatalf("Expected *rpc.RegisterResponse for subscriber1, got %T", sub1Resp)
	}
	sub1ID := sub1RegResp.AgentID

	// Subscriber 2
	sub2Req := rpc.RegisterRequest{Role: "subscriber2", Module: "test"}
	sub2ReqJSON, err := json.Marshal(sub2Req)
	if err != nil {
		t.Fatalf("Failed to marshal sub2 request: %v", err)
	}
	sub2Resp, err := agentHandler.HandleRegister(context.Background(), sub2ReqJSON)
	if err != nil {
		t.Fatalf("Failed to register subscriber2 agent: %v", err)
	}
	sub2RegResp, ok := sub2Resp.(*rpc.RegisterResponse)
	if !ok {
		t.Fatalf("Expected *rpc.RegisterResponse for subscriber2, got %T", sub2Resp)
	}
	sub2ID := sub2RegResp.AgentID

	// Start sessions
	sessionHandler := rpc.NewSessionHandler(st)

	// Sender session
	senderSessionReq := rpc.SessionStartRequest{AgentID: senderID}
	senderSessionReqJSON, err := json.Marshal(senderSessionReq)
	if err != nil {
		t.Fatalf("Failed to marshal sender session request: %v", err)
	}
	senderSessionResp, err := sessionHandler.HandleStart(context.Background(), senderSessionReqJSON)
	if err != nil {
		t.Fatalf("Failed to start sender session: %v", err)
	}
	senderStartResp, ok := senderSessionResp.(*rpc.SessionStartResponse)
	if !ok {
		t.Fatalf("Expected *rpc.SessionStartResponse for sender, got %T", senderSessionResp)
	}
	_ = senderStartResp.SessionID

	// Subscriber 1 session
	session1Req := rpc.SessionStartRequest{AgentID: sub1ID}
	session1ReqJSON, err := json.Marshal(session1Req)
	if err != nil {
		t.Fatalf("Failed to marshal session1 request: %v", err)
	}
	session1Resp, err := sessionHandler.HandleStart(context.Background(), session1ReqJSON)
	if err != nil {
		t.Fatalf("Failed to start session1: %v", err)
	}
	session1StartResp, ok := session1Resp.(*rpc.SessionStartResponse)
	if !ok {
		t.Fatalf("Expected *rpc.SessionStartResponse for session1, got %T", session1Resp)
	}
	session1ID := session1StartResp.SessionID

	// Subscriber 2 session
	session2Req := rpc.SessionStartRequest{AgentID: sub2ID}
	session2ReqJSON, err := json.Marshal(session2Req)
	if err != nil {
		t.Fatalf("Failed to marshal session2 request: %v", err)
	}
	session2Resp, err := sessionHandler.HandleStart(context.Background(), session2ReqJSON)
	if err != nil {
		t.Fatalf("Failed to start session2: %v", err)
	}
	session2StartResp, ok := session2Resp.(*rpc.SessionStartResponse)
	if !ok {
		t.Fatalf("Expected *rpc.SessionStartResponse for session2, got %T", session2Resp)
	}
	session2ID := session2StartResp.SessionID

	// Create filtered subscription - session1 subscribes to task:thrum-ukr scope only
	subService := subscriptions.NewService(st.DB())
	scope := &types.Scope{Type: "task", Value: "thrum-ukr"}
	_, err = subService.Subscribe(context.Background(), session1ID, scope, nil, false)
	if err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	// Create subscription for session2 - all messages
	_, err = subService.Subscribe(context.Background(), session2ID, nil, nil, true)
	if err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	// Create mock receivers for both sessions
	receiver1 := &mockNotificationReceiver{}
	receiver2 := &mockNotificationReceiver{}

	// Create a multi-session notifier
	multiNotifier := &multiSessionNotifier{
		receivers: map[string]*mockNotificationReceiver{
			session1ID: receiver1,
			session2ID: receiver2,
		},
	}

	// Create dispatcher with the multi-notifier
	dispatcher := subscriptions.NewDispatcher(st.DB())
	dispatcher.SetClientNotifier(multiNotifier)

	// Create message handler
	messageHandler := rpc.NewMessageHandlerWithDispatcher(st, dispatcher)

	// Send message with matching scope (task:thrum-ukr)
	sendReq1 := rpc.SendRequest{
		Content: "Message with matching scope",
		Scopes: []types.Scope{
			{Type: "task", Value: "thrum-ukr"},
		},
	}
	sendReq1JSON, err := json.Marshal(sendReq1)
	if err != nil {
		t.Fatalf("Failed to marshal sendReq1: %v", err)
	}
	_, err = messageHandler.HandleSend(context.Background(), sendReq1JSON)
	if err != nil {
		t.Fatalf("Failed to send message 1: %v", err)
	}

	// Send message with different scope (task:other)
	sendReq2 := rpc.SendRequest{
		Content: "Message with different scope",
		Scopes: []types.Scope{
			{Type: "task", Value: "other"},
		},
	}
	sendReq2JSON, err := json.Marshal(sendReq2)
	if err != nil {
		t.Fatalf("Failed to marshal sendReq2: %v", err)
	}
	_, err = messageHandler.HandleSend(context.Background(), sendReq2JSON)
	if err != nil {
		t.Fatalf("Failed to send message 2: %v", err)
	}

	// Give notifications time to be processed
	time.Sleep(10 * time.Millisecond)

	// Verify session1 only received the matching message
	if len(receiver1.notifications) != 1 {
		t.Errorf("Session1 expected 1 notification (filtered), got %d", len(receiver1.notifications))
	}

	// Verify session2 received both messages (subscribed to all)
	if len(receiver2.notifications) != 2 {
		t.Errorf("Session2 expected 2 notifications (all), got %d", len(receiver2.notifications))
	}
}

// multiSessionNotifier routes notifications to different mock receivers by session ID.
type multiSessionNotifier struct {
	receivers map[string]*mockNotificationReceiver
}

func (m *multiSessionNotifier) Notify(sessionID string, notification any) error {
	if receiver, ok := m.receivers[sessionID]; ok {
		return receiver.Notify(sessionID, notification)
	}
	return nil
}
