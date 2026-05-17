package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	email "github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/schema"
)

// --- test helpers ---

// openTestDB creates an in-memory SQLite DB with the full schema applied.
func openTestEmailDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "email_rpc.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// insertTestAgent writes a minimal agent row so requireAgentRegistered passes.
func insertTestAgent(t *testing.T, db *sql.DB, agentID, role string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO agents (agent_id, kind, role, module, display, registered_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		agentID, "agent", role, "test", agentID, now, now,
	)
	if err != nil {
		t.Fatalf("insertTestAgent %s: %v", agentID, err)
	}
}

// setupMeshConfigForRPC creates a temp config.json and WAL for the mesh handler.
func setupMeshConfigForRPC(t *testing.T, peers []config.EmailPeer) (configDir string, configPath string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	peersJSON, _ := json.Marshal(peers)
	data := []byte(`{"daemon":{"local_only":true},"email":{"enabled":true,"peers":` + string(peersJSON) + `}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir, path
}

// newTestMesh builds a MeshHandlerImpl pointing at a temp config.json.
func newTestMesh(t *testing.T, configPath string) *email.MeshHandlerImpl {
	t.Helper()
	walPath := filepath.Join(t.TempDir(), "mesh.jsonl")
	wal, err := state.NewPendingMeshUpdatesLog(walPath)
	if err != nil {
		t.Fatalf("new wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })
	cfg := email.MeshConfig{
		MyDaemonID:    "test-daemon-0000",
		MyDaemonShort: "test-dae",
		ConfigPath:    configPath,
		PairPendingTTL: 24 * time.Hour,
	}
	return email.NewMeshHandler(cfg, wal, nil, nil)
}

// stubBridge is a minimal EmailBridgeInterface for unit tests.
// Tests populate only the fields they care about; zero values are safe.
type stubBridge struct {
	cfg     config.EmailConfig
	status  email.BridgeStatus
	queue   *email.Queue
	mesh    *email.MeshHandlerImpl
	limiter *email.Limiter
}

func (s *stubBridge) Status() email.BridgeStatus     { return s.status }
func (s *stubBridge) Queue() *email.Queue             { return s.queue }
func (s *stubBridge) Mesh() *email.MeshHandlerImpl    { return s.mesh }
func (s *stubBridge) Limiter() *email.Limiter         { return s.limiter }
func (s *stubBridge) Config() config.EmailConfig      { return s.cfg }

// --- tests ---

// TestRpcEmail_SendQueuesQueued verifies that a valid send request inserts a
// queued row and returns a non-zero QueueID plus a Message-Id in the expected
// thrum format.
func TestRpcEmail_SendQueuesQueued(t *testing.T) {
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "implementer_test", "implementer")

	bridge := &stubBridge{
		cfg:   config.EmailConfig{Enabled: true, DaemonHandle: "myhandle", SMTP: config.EmailSMTP{Host: "smtp.example.com"}},
		queue: email.NewQueue(db),
	}
	h := NewEmailHandler(bridge, db)

	req := EmailSendRequest{
		CallerAgentID: "implementer_test",
		ToAddress:     "alice@example.com",
		Subject:       "hello",
		Body:          "test body",
	}
	raw, _ := json.Marshal(req)

	resp, err := h.HandleSend(context.Background(), raw)
	if err != nil {
		t.Fatalf("HandleSend: %v", err)
	}

	sr, ok := resp.(*EmailSendResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", resp)
	}
	if sr.Status != "queued" {
		t.Errorf("status=%q; want queued", sr.Status)
	}
	if sr.QueueID <= 0 {
		t.Errorf("QueueID=%d; want >0", sr.QueueID)
	}
	if !strings.HasPrefix(sr.MessageID, "<thrum-") {
		t.Errorf("MessageID=%q; want <thrum-...@...> format", sr.MessageID)
	}
	if !strings.Contains(sr.MessageID, "@smtp.example.com>") {
		t.Errorf("MessageID=%q; want @smtp.example.com> suffix", sr.MessageID)
	}

	// Verify the row is in the DB with status=queued.
	var status string
	err = db.QueryRow(`SELECT status FROM email_outbound_queue WHERE id = ?`, sr.QueueID).Scan(&status)
	if err != nil {
		t.Fatalf("select queue row: %v", err)
	}
	if status != "queued" {
		t.Errorf("db status=%q; want queued", status)
	}
}

// TestRpcEmail_SendUnauthorizedNoAgent verifies that an empty or unknown
// caller_agent_id is rejected with an "unauthorized" error.
func TestRpcEmail_SendUnauthorizedNoAgent(t *testing.T) {
	db := openTestEmailDB(t)

	bridge := &stubBridge{
		cfg:   config.EmailConfig{Enabled: true},
		queue: email.NewQueue(db),
	}
	h := NewEmailHandler(bridge, db)

	cases := []struct {
		name    string
		callerID string
	}{
		{"empty", ""},
		{"unknown", "ghost_agent"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := EmailSendRequest{
				CallerAgentID: tc.callerID,
				ToAddress:     "alice@example.com",
				Body:          "test",
			}
			raw, _ := json.Marshal(req)
			_, err := h.HandleSend(context.Background(), raw)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "unauthorized") {
				t.Errorf("error=%q; want 'unauthorized'", err)
			}
		})
	}
}

// TestRpcEmail_SendOverSizeRejects verifies that a body exceeding
// MaxOutboundBytes is rejected with over_size_limit.
func TestRpcEmail_SendOverSizeRejects(t *testing.T) {
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "impl_agent", "implementer")

	bridge := &stubBridge{
		cfg:   config.EmailConfig{Enabled: true, MaxOutboundBytes: 100},
		queue: email.NewQueue(db),
	}
	h := NewEmailHandler(bridge, db)

	req := EmailSendRequest{
		CallerAgentID: "impl_agent",
		ToAddress:     "alice@example.com",
		Body:          strings.Repeat("x", 101),
	}
	raw, _ := json.Marshal(req)

	_, err := h.HandleSend(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "over_size_limit") {
		t.Errorf("error=%q; want over_size_limit", err)
	}
}

// TestRpcEmail_SendBridgeDisabledRejects verifies that email.send returns
// bridge_disabled when cfg.Enabled=false.
func TestRpcEmail_SendBridgeDisabledRejects(t *testing.T) {
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "impl_agent", "implementer")

	bridge := &stubBridge{
		cfg: config.EmailConfig{Enabled: false},
	}
	h := NewEmailHandler(bridge, db)

	req := EmailSendRequest{
		CallerAgentID: "impl_agent",
		ToAddress:     "alice@example.com",
		Body:          "hello",
	}
	raw, _ := json.Marshal(req)

	_, err := h.HandleSend(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "bridge_disabled") {
		t.Errorf("error=%q; want bridge_disabled", err)
	}
}

// TestRpcEmail_PeerPairCoordinatorOnly verifies that a non-coordinator caller
// is rejected by email.peer.pair.
func TestRpcEmail_PeerPairCoordinatorOnly(t *testing.T) {
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "impl_agent", "implementer") // not coordinator

	bridge := &stubBridge{
		cfg: config.EmailConfig{Enabled: true},
	}
	h := NewEmailHandler(bridge, db)

	req := EmailPeerPairRequest{
		CallerAgentID: "impl_agent",
		ToHandle:      "alice",
	}
	raw, _ := json.Marshal(req)

	_, err := h.HandlePeerPair(context.Background(), raw)
	if err == nil {
		t.Fatal("expected unauthorized error, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error=%q; want unauthorized", err)
	}
}

// TestRpcEmail_PeerListReturnsConfigPeers verifies that email.peer.list
// returns all peers from the bridge config with correct field mapping.
func TestRpcEmail_PeerListReturnsConfigPeers(t *testing.T) {
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "coord_main", "coordinator")

	peers := []config.EmailPeer{
		{Handle: "alice", DaemonID: "abcdef1234567890", VouchedBy: "self", Trust: "full", AddedAt: "2024-01-01T00:00:00Z"},
		{Handle: "bob", DaemonID: "0011223344556677", VouchedBy: "alice", Trust: "limited", AddedAt: "2024-02-01T00:00:00Z"},
	}

	bridge := &stubBridge{
		cfg: config.EmailConfig{Enabled: true, Peers: peers},
	}
	h := NewEmailHandler(bridge, db)

	req := EmailPeerListRequest{CallerAgentID: "coord_main"}
	raw, _ := json.Marshal(req)

	resp, err := h.HandlePeerList(context.Background(), raw)
	if err != nil {
		t.Fatalf("HandlePeerList: %v", err)
	}

	lr, ok := resp.(*EmailPeerListResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", resp)
	}
	if len(lr.Peers) != 2 {
		t.Fatalf("len(peers)=%d; want 2", len(lr.Peers))
	}

	alice := lr.Peers[0]
	if alice.Handle != "alice" {
		t.Errorf("peer[0].Handle=%q; want alice", alice.Handle)
	}
	if alice.DaemonIDShort != "abcdef12" {
		t.Errorf("peer[0].DaemonIDShort=%q; want abcdef12", alice.DaemonIDShort)
	}
	if alice.VouchedBy != "self" {
		t.Errorf("peer[0].VouchedBy=%q; want self", alice.VouchedBy)
	}
	if alice.Trust != "full" {
		t.Errorf("peer[0].Trust=%q; want full", alice.Trust)
	}
}

// TestRpcEmail_PeerRevokeGossips verifies that email.peer.revoke removes the
// named peer from config.json via the mesh handler.
func TestRpcEmail_PeerRevokeGossips(t *testing.T) {
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "coord_main", "coordinator")

	initialPeers := []config.EmailPeer{
		{Handle: "alice", DaemonID: "aaa", VouchedBy: "self", Trust: "full"},
		{Handle: "bob", DaemonID: "bbb", VouchedBy: "self", Trust: "full"},
	}
	_, configPath := setupMeshConfigForRPC(t, initialPeers)
	mesh := newTestMesh(t, configPath)

	bridge := &stubBridge{
		cfg:  config.EmailConfig{Enabled: true},
		mesh: mesh,
	}
	h := NewEmailHandler(bridge, db)

	req := EmailPeerRevokeRequest{
		CallerAgentID: "coord_main",
		ToHandle:      "alice",
	}
	raw, _ := json.Marshal(req)

	resp, err := h.HandlePeerRevoke(context.Background(), raw)
	if err != nil {
		t.Fatalf("HandlePeerRevoke: %v", err)
	}
	rr, ok := resp.(*EmailPeerRevokeResponse)
	if !ok {
		t.Fatalf("unexpected type %T", resp)
	}
	if !rr.Removed {
		t.Error("Removed=false; want true")
	}

	// Verify alice is gone from config.json.
	thrumCfg, err := config.LoadThrumConfig(filepath.Dir(configPath))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	for _, p := range thrumCfg.Email.Peers {
		if p.Handle == "alice" {
			t.Error("alice still present in config after revoke")
		}
	}
	// bob must still be there.
	var bobFound bool
	for _, p := range thrumCfg.Email.Peers {
		if p.Handle == "bob" {
			bobFound = true
		}
	}
	if !bobFound {
		t.Error("bob missing from config after alice revoke")
	}
}

// TestRpcEmail_PeerRebindUpdatesHandle verifies that email.peer.rebind swaps
// the daemon_id under an existing handle in config.json.
func TestRpcEmail_PeerRebindUpdatesHandle(t *testing.T) {
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "coord_main", "coordinator")

	initialPeers := []config.EmailPeer{
		{Handle: "alice", DaemonID: "old-id-aaa", VouchedBy: "self", Trust: "full"},
	}
	_, configPath := setupMeshConfigForRPC(t, initialPeers)
	mesh := newTestMesh(t, configPath)

	// Seed a pending rebind so ConfirmRebind has something to confirm.
	alicePayload := email.PeerProtocolPayload{
		Handle:      "alice",
		DaemonID:    "old-id-aaa",
		NewDaemonID: "new-id-bbb",
	}
	if err := mesh.HandlePeerRebind(context.Background(), alicePayload); err != nil {
		t.Fatalf("seed HandlePeerRebind: %v", err)
	}

	bridge := &stubBridge{
		cfg:  config.EmailConfig{Enabled: true},
		mesh: mesh,
	}
	h := NewEmailHandler(bridge, db)

	req := EmailPeerRebindRequest{
		CallerAgentID: "coord_main",
		ToHandle:      "alice",
		NewDaemonID:   "new-id-bbb",
	}
	raw, _ := json.Marshal(req)

	resp, err := h.HandlePeerRebind(context.Background(), raw)
	if err != nil {
		t.Fatalf("HandlePeerRebind: %v", err)
	}
	rr, ok := resp.(*EmailPeerRebindResponse)
	if !ok {
		t.Fatalf("unexpected type %T", resp)
	}
	if !rr.Updated {
		t.Error("Updated=false; want true")
	}

	// Verify new daemon_id is in config.json.
	thrumCfg, err := config.LoadThrumConfig(filepath.Dir(configPath))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	var found bool
	for _, p := range thrumCfg.Email.Peers {
		if p.Handle == "alice" {
			found = true
			if p.DaemonID != "new-id-bbb" {
				t.Errorf("alice.DaemonID=%q; want new-id-bbb", p.DaemonID)
			}
		}
	}
	if !found {
		t.Error("alice not found in config after rebind")
	}
}

// TestRpcEmail_StatusFieldsPopulated verifies that email.status returns a
// response with all fields populated. Running=false because the bridge is a
// stub (not Run). OutboundQueueDepth reflects pre-inserted rows.
func TestRpcEmail_StatusFieldsPopulated(t *testing.T) {
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "coord_main", "coordinator")

	// Insert two queued rows so depth = 2.
	q := email.NewQueue(db)
	for i := 0; i < 2; i++ {
		_, err := q.Enqueue(context.Background(), email.QueueEnvelope{
			FromAgent: "coord_main",
			ToAddress: "alice@example.com",
			Body:      "test",
		})
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	limiter := email.NewLimiter(db, email.LimiterConfig{
		InboundPerPeerPerHour: 10, OutboundPerPeerPerHour: 10,
	}, nil)

	bridge := &stubBridge{
		cfg:     config.EmailConfig{Enabled: true},
		status:  email.BridgeStatus{Running: false, InboundProcessed: 5},
		queue:   q,
		limiter: limiter,
	}
	h := NewEmailHandler(bridge, db)

	req := EmailStatusRequest{CallerAgentID: "coord_main"}
	raw, _ := json.Marshal(req)

	resp, err := h.HandleStatus(context.Background(), raw)
	if err != nil {
		t.Fatalf("HandleStatus: %v", err)
	}
	sr, ok := resp.(*EmailStatusResponse)
	if !ok {
		t.Fatalf("unexpected type %T", resp)
	}

	if sr.Running {
		t.Error("Running=true; want false (bridge not started)")
	}
	if sr.InboundCount != 5 {
		t.Errorf("InboundCount=%d; want 5", sr.InboundCount)
	}
	if sr.OutboundQueueDepth != 2 {
		t.Errorf("OutboundQueueDepth=%d; want 2", sr.OutboundQueueDepth)
	}
	if sr.PausedPeers == nil {
		t.Error("PausedPeers=nil; want []string")
	}
}

// TestRpcEmail_UnblockClearsRateState verifies that email.unblock clears a
// peer's pause flag in both the in-memory Limiter and SQLite.
func TestRpcEmail_UnblockClearsRateState(t *testing.T) {
	db := openTestEmailDB(t)
	insertTestAgent(t, db, "coord_main", "coordinator")

	limiter := email.NewLimiter(db, email.LimiterConfig{
		InboundPerPeerPerHour:  1, // low threshold so one call triggers pause
		OutboundPerPeerPerHour: 10,
		GlobalInboundPerMinute: 100,
	}, nil)

	// Trigger a pause by exhausting the per-peer inbound quota.
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, _, _ = limiter.IncrementInbound(ctx, "peer-alice")
	}

	paused, err := limiter.IsPaused(ctx, "peer-alice")
	if err != nil {
		t.Fatalf("IsPaused: %v", err)
	}
	if !paused {
		t.Fatal("expected peer-alice to be paused before unblock")
	}

	bridge := &stubBridge{
		cfg:     config.EmailConfig{Enabled: true},
		limiter: limiter,
	}
	h := NewEmailHandler(bridge, db)

	req := EmailUnblockRequest{
		CallerAgentID: "coord_main",
		PeerKey:       "peer-alice",
	}
	raw, _ := json.Marshal(req)

	resp, err := h.HandleUnblock(ctx, raw)
	if err != nil {
		t.Fatalf("HandleUnblock: %v", err)
	}
	ur, ok := resp.(*EmailUnblockResponse)
	if !ok {
		t.Fatalf("unexpected type %T", resp)
	}
	if !ur.Unblocked {
		t.Error("Unblocked=false; want true")
	}

	// Verify the limiter no longer reports the peer as paused.
	paused, err = limiter.IsPaused(ctx, "peer-alice")
	if err != nil {
		t.Fatalf("IsPaused after unblock: %v", err)
	}
	if paused {
		t.Error("peer-alice still paused after unblock")
	}
}
