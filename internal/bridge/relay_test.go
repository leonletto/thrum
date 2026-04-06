package bridge_test

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"testing"

	"github.com/leonletto/thrum/internal/bridge"
)

// rpcRecorder records Call() invocations for assertion.
type rpcRecorder struct {
	mockTransport
	calls []rpcCall
}

type rpcCall struct {
	Method string
	Params map[string]any
}

func (r *rpcRecorder) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	r.calls = append(r.calls, rpcCall{Method: method, Params: params})
	// group.list returns {"groups":[]}
	if method == "group.list" {
		return json.RawMessage(`{"groups":[]}`), nil
	}
	return json.RawMessage(`{"message_id":"thrum-123"}`), nil
}

func newRecorder() *rpcRecorder {
	return &rpcRecorder{
		mockTransport: mockTransport{name: "test", connected: true, notifyCh: make(chan bridge.Notification)},
	}
}

func testLogger() *log.Logger {
	return log.New(os.Stderr, "test: ", 0)
}

// --- Inbound tests ---

func TestRelay_InboundDM(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	msgMap := bridge.NewMessageMap(100)
	cfg := bridge.RelayConfig{
		BridgeUserID: "user:bridge",
		Target:       "@coordinator_main",
	}
	relay := bridge.NewRelay(rec, msgMap, cfg, testLogger())

	msg := bridge.InboundMessage{
		Content:     "hello from outside",
		SenderID:    "user:alice",
		ExternalKey: "ext-1",
		Structured:  map[string]any{"source": "test"},
	}

	if err := relay.RelayInbound(context.Background(), msg); err != nil {
		t.Fatalf("RelayInbound() = %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 RPC call, got %d", len(rec.calls))
	}
	if rec.calls[0].Method != "message.send" {
		t.Fatalf("method = %q, want message.send", rec.calls[0].Method)
	}
	mentions, _ := rec.calls[0].Params["mentions"].([]string)
	if len(mentions) != 1 || mentions[0] != "@coordinator_main" {
		t.Fatalf("mentions = %v, want [@coordinator_main]", mentions)
	}
	if id, ok := msgMap.ThrumID("ext-1"); !ok || id != "thrum-123" {
		t.Fatalf("msgMap.ThrumID(ext-1) = %q, %v", id, ok)
	}
}

func TestRelay_InboundGroup(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	msgMap := bridge.NewMessageMap(100)
	cfg := bridge.RelayConfig{
		BridgeUserID: "user:bridge",
		Groups:       []bridge.GroupConfig{{Name: "team", ThrumName: "peer:team"}},
	}
	relay := bridge.NewRelay(rec, msgMap, cfg, testLogger())

	msg := bridge.InboundMessage{
		Content:     "hello group",
		SenderID:    "user:alice",
		ExternalKey: "ext-2",
		GroupName:   "team",
	}

	if err := relay.RelayInbound(context.Background(), msg); err != nil {
		t.Fatalf("RelayInbound() = %v", err)
	}

	mentions, _ := rec.calls[0].Params["mentions"].([]string)
	if len(mentions) != 1 || mentions[0] != "peer:team" {
		t.Fatalf("mentions = %v, want [peer:team]", mentions)
	}
}

func TestRelay_InboundReplyThreading(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	msgMap := bridge.NewMessageMap(100)
	// Pre-populate a mapping for reply threading.
	msgMap.Store("parent-ext-key", "thrum-parent-1")

	cfg := bridge.RelayConfig{
		BridgeUserID: "user:bridge",
		Target:       "@coordinator_main",
	}
	relay := bridge.NewRelay(rec, msgMap, cfg, testLogger())

	msg := bridge.InboundMessage{
		Content:     "replying",
		SenderID:    "user:alice",
		ExternalKey: "ext-3",
		ReplyToKey:  "parent-ext-key",
	}

	if err := relay.RelayInbound(context.Background(), msg); err != nil {
		t.Fatalf("RelayInbound() = %v", err)
	}

	replyTo, _ := rec.calls[0].Params["reply_to"].(string)
	if replyTo != "thrum-parent-1" {
		t.Fatalf("reply_to = %q, want thrum-parent-1", replyTo)
	}
}

func TestRelay_InboundUnknownGroup(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	cfg := bridge.RelayConfig{BridgeUserID: "user:bridge"}
	relay := bridge.NewRelay(rec, bridge.NewMessageMap(100), cfg, testLogger())

	msg := bridge.InboundMessage{
		Content:   "hello",
		GroupName: "nonexistent",
	}

	err := relay.RelayInbound(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for unknown group")
	}
}

// --- Outbound classification tests ---

func TestRelay_ClassifyOutbound_Echo(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	cfg := bridge.RelayConfig{BridgeUserID: "user:bridge"}
	relay := bridge.NewRelay(rec, bridge.NewMessageMap(100), cfg, testLogger())

	route := relay.ClassifyOutbound("user:bridge", []string{"someone"}, "")
	if route.Type != bridge.RouteSkip {
		t.Fatalf("route.Type = %v, want RouteSkip", route.Type)
	}
}

func TestRelay_ClassifyOutbound_Group(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	cfg := bridge.RelayConfig{
		BridgeUserID: "user:bridge",
		Groups:       []bridge.GroupConfig{{Name: "team", ThrumName: "peer:team"}},
	}
	relay := bridge.NewRelay(rec, bridge.NewMessageMap(100), cfg, testLogger())

	route := relay.ClassifyOutbound("user:alice", []string{"peer:team"}, "")
	if route.Type != bridge.RouteGroup {
		t.Fatalf("route.Type = %v, want RouteGroup", route.Type)
	}
	if route.GroupName != "team" {
		t.Fatalf("route.GroupName = %q, want team", route.GroupName)
	}
}

func TestRelay_ClassifyOutbound_ReplyToGroup(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	msgMap := bridge.NewMessageMap(100)
	msgMap.Store("ext-key-1", "thrum-msg-1")

	cfg := bridge.RelayConfig{BridgeUserID: "user:bridge"}
	relay := bridge.NewRelay(rec, msgMap, cfg, testLogger())

	route := relay.ClassifyOutbound("user:alice", []string{"someone"}, "thrum-msg-1")
	if route.Type != bridge.RouteReplyToGroup {
		t.Fatalf("route.Type = %v, want RouteReplyToGroup", route.Type)
	}
	if route.ExternalKey != "ext-key-1" {
		t.Fatalf("route.ExternalKey = %q, want ext-key-1", route.ExternalKey)
	}
}

func TestRelay_ClassifyOutbound_Proxy(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	cfg := bridge.RelayConfig{
		BridgeUserID: "user:bridge",
		Proxies:      []bridge.ProxyConfig{{Prefix: "mock-sf", AgentName: "coordinator_main", GroupName: "team"}},
	}
	relay := bridge.NewRelay(rec, bridge.NewMessageMap(100), cfg, testLogger())

	route := relay.ClassifyOutbound("user:alice", []string{"mock-sf:coordinator_main"}, "")
	if route.Type != bridge.RouteProxy {
		t.Fatalf("route.Type = %v, want RouteProxy", route.Type)
	}
	if route.ProxyAgent != "mock-sf:coordinator_main" {
		t.Fatalf("route.ProxyAgent = %q, want mock-sf:coordinator_main", route.ProxyAgent)
	}
}

func TestRelay_ClassifyOutbound_DM(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	cfg := bridge.RelayConfig{BridgeUserID: "user:bridge"}
	relay := bridge.NewRelay(rec, bridge.NewMessageMap(100), cfg, testLogger())

	route := relay.ClassifyOutbound("user:alice", []string{"user:bridge"}, "")
	if route.Type != bridge.RouteDM {
		t.Fatalf("route.Type = %v, want RouteDM", route.Type)
	}
}

// --- EnsureProxies tests ---

func TestRelay_EnsureProxies_CreatesGroup(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	cfg := bridge.RelayConfig{
		BridgeUserID: "user:bridge",
		Target:       "@coordinator_main",
		Groups:       []bridge.GroupConfig{{Name: "team", ThrumName: "peer:team"}},
	}
	relay := bridge.NewRelay(rec, bridge.NewMessageMap(100), cfg, testLogger())

	if err := relay.EnsureProxies(context.Background()); err != nil {
		t.Fatalf("EnsureProxies() = %v", err)
	}

	// Should have called: group.list, group.create, group.member.add (bridge user), group.member.add (target)
	methods := make([]string, len(rec.calls))
	for i, c := range rec.calls {
		methods[i] = c.Method
	}

	// Verify group.list was called
	found := false
	for _, m := range methods {
		if m == "group.list" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected group.list call")
	}

	// Verify group.create was called (since our mock returns empty groups)
	found = false
	for _, m := range methods {
		if m == "group.create" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected group.create call")
	}

	// Verify group.member.add uses correct params (member_type + member_value, NOT agent)
	for _, c := range rec.calls {
		if c.Method == "group.member.add" {
			if _, ok := c.Params["agent"]; ok {
				t.Fatal("group.member.add should use member_type/member_value, not agent")
			}
			if c.Params["member_type"] != "agent" {
				t.Fatalf("member_type = %v, want agent", c.Params["member_type"])
			}
		}
	}
}

func TestRelay_EnsureProxies_RegistersAgent(t *testing.T) {
	t.Parallel()
	rec := newRecorder()
	cfg := bridge.RelayConfig{
		BridgeUserID: "user:bridge",
		Groups:       []bridge.GroupConfig{{Name: "team", ThrumName: "peer:team"}},
		Proxies:      []bridge.ProxyConfig{{Prefix: "mock-sf", AgentName: "coordinator_main", GroupName: "team"}},
	}
	relay := bridge.NewRelay(rec, bridge.NewMessageMap(100), cfg, testLogger())

	if err := relay.EnsureProxies(context.Background()); err != nil {
		t.Fatalf("EnsureProxies() = %v", err)
	}

	// Find agent.register call
	found := false
	for _, c := range rec.calls {
		if c.Method == "agent.register" {
			found = true
			if c.Params["name"] != "mock-sf:coordinator_main" {
				t.Fatalf("agent name = %v, want mock-sf:coordinator_main", c.Params["name"])
			}
		}
	}
	if !found {
		t.Fatal("expected agent.register call")
	}
}
