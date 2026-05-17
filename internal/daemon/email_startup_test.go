package daemon

// Tests for D-B1.17: email bridge + handler wiring at daemon startup.
//
// These tests validate the startup-time gating logic (enabled-flag, missing
// secrets → fatal) and the RPC routing (email.* methods reach the handlers
// after wiring). They deliberately avoid standing up a full daemon process:
// the test-harness creates a Server, registers handlers exactly as
// runDaemon() does, and calls methods over the Unix socket.
//
// The runDaemon() function itself is in the `main` package (cmd/thrum/main.go)
// and is not directly importable. Full end-to-end startup tests belong in the
// e2e test plan; these unit-level tests cover the wiring contract.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	emailbridge "github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/schema"
)

// --- helpers ---

// openEmailTestDB creates a file-backed SQLite DB with the full schema.
func openEmailTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "email_startup.db"))
	if err != nil {
		t.Fatalf("schema.OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("schema.InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// writeEmailSecrets writes a valid secrets file at path (mode 0600).
func writeEmailSecrets(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir secrets dir: %v", err)
	}
	data := []byte(`{"imap_password":"test-imap-pw","smtp_password":"test-smtp-pw"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write secrets: %v", err)
	}
}

// startServerWithEmailHandlers creates a Server, registers the 7 email.*
// handlers (mirroring runDaemon wiring), starts it, and returns the socket
// path. The emailBridge passed in may be a concrete *emailbridge.Bridge or
// the stubEmailBridge below.
//
// os.MkdirTemp("", "emls") creates a short directory under /tmp (macOS has a
// 104-byte Unix-socket-path limit; t.TempDir() produces paths ~120+ chars).
func startServerWithEmailHandlers(
	t *testing.T,
	bridge rpc.EmailBridgeInterface,
	db *sql.DB,
) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "emls")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath := filepath.Join(tmpDir, "t.sock")

	server := NewServer(socketPath)
	emailHandler := rpc.NewEmailHandler(bridge, db)
	server.RegisterHandler("email.send", emailHandler.HandleSend)
	server.RegisterHandler("email.peer.pair", emailHandler.HandlePeerPair)
	server.RegisterHandler("email.peer.list", emailHandler.HandlePeerList)
	server.RegisterHandler("email.peer.revoke", emailHandler.HandlePeerRevoke)
	server.RegisterHandler("email.peer.rebind", emailHandler.HandlePeerRebind)
	server.RegisterHandler("email.status", emailHandler.HandleStatus)
	server.RegisterHandler("email.unblock", emailHandler.HandleUnblock)

	ctx, cancel := context.WithCancel(context.Background())
	if err := server.Start(ctx); err != nil {
		cancel()
		t.Fatalf("server.Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = server.Stop()
	})
	waitForSocketReady(t, socketPath)
	return socketPath
}

// callMethod sends a JSON-RPC 2.0 request over the Unix socket and returns
// the raw result bytes or the error string.
func callMethod(t *testing.T, socketPath, method string, params any) (json.RawMessage, string) {
	t.Helper()
	result, msg, _ := callMethodFull(t, socketPath, method, params)
	return result, msg
}

// callMethodFull returns (result, errMsg, errCode) for callers that need to
// distinguish JSON-RPC error codes (e.g. -32601 Method not found vs -32000
// application error).
func callMethodFull(t *testing.T, socketPath, method string, params any) (json.RawMessage, string, int) {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != nil {
		return nil, resp.Error.Message, resp.Error.Code
	}
	return resp.Result, "", 0
}

// stubEmailBridge is a minimal EmailBridgeInterface for startup tests.
// It satisfies the interface without standing up a real bridge.
type stubEmailBridge struct {
	cfg     config.EmailConfig
	status  emailbridge.BridgeStatus
	queue   *emailbridge.Queue
	mesh    *emailbridge.MeshHandlerImpl
	limiter *emailbridge.Limiter
	inbound *emailbridge.Inbound
}

func (s *stubEmailBridge) Status() emailbridge.BridgeStatus   { return s.status }
func (s *stubEmailBridge) Queue() *emailbridge.Queue          { return s.queue }
func (s *stubEmailBridge) Mesh() *emailbridge.MeshHandlerImpl { return s.mesh }
func (s *stubEmailBridge) Limiter() *emailbridge.Limiter      { return s.limiter }
func (s *stubEmailBridge) Inbound() *emailbridge.Inbound      { return s.inbound }
func (s *stubEmailBridge) Config() config.EmailConfig         { return s.cfg }

// --- tests ---

// TestDaemonStartup_EmailDisabledNoBridge verifies that when Email.Enabled is
// false the daemon can start without a secrets file and the email.status RPC
// returns a disabled-state response (bridge not running, no error).
//
// A nil DB is passed to NewEmailHandler so requireAgentRegistered skips the
// agents table lookup — this mirrors the test-scaffold path documented in the
// handler's own comment ("DB not available — allow in test scenarios").
func TestDaemonStartup_EmailDisabledNoBridge(t *testing.T) {
	// With Email.Enabled=false the daemon passes nil secrets to the bridge.
	// The handler still registers; status returns running=false.
	disabledBridge := &stubEmailBridge{
		cfg: config.EmailConfig{Enabled: false},
	}
	// nil DB: auth bypass (see requireAgentRegistered comment).
	socketPath := startServerWithEmailHandlers(t, disabledBridge, nil)

	result, errMsg := callMethod(t, socketPath, "email.status", map[string]any{
		"caller_agent_id": "user:test",
	})
	if errMsg != "" {
		t.Fatalf("email.status returned error: %s", errMsg)
	}

	var resp struct {
		Running bool `json:"running"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Running {
		t.Error("expected bridge not running when Email.Enabled=false")
	}
}

// TestDaemonStartup_EmailEnabledMissingSecretsFatal verifies that when
// Email.Enabled=true and the secrets file is absent, LoadEmailSecrets returns
// ErrEmailSecretsMissing — the error the daemon uses to gate startup.
//
// The daemon's runDaemon function (cmd/thrum/main.go) passes this error up as
// a fatal startup error. Here we test the config-layer gate directly because
// runDaemon() lives in the main package and is not importable from tests.
func TestDaemonStartup_EmailEnabledMissingSecretsFatal(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "nonexistent", "email.json")

	_, err := config.LoadEmailSecrets(missingPath, true /* enabled */)
	if err == nil {
		t.Fatal("expected error for missing secrets file with Email.Enabled=true")
	}
	if !isEmailSecretsMissing(err) {
		t.Fatalf("expected ErrEmailSecretsMissing chain, got: %v", err)
	}
}

// isEmailSecretsMissing reports whether err wraps config.ErrEmailSecretsMissing.
func isEmailSecretsMissing(err error) bool {
	// errors.Is would work; use string containment as a fallback to avoid
	// importing errors just for one check.
	return containsString(err.Error(), "email secrets file not found")
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStringInner(s, sub))
}

func containsStringInner(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestDaemonStartup_EmailEnabledHappy verifies that when Email.Enabled=true
// and valid secrets exist, LoadEmailSecrets succeeds and the bridge + RPC
// handlers can be wired without error. The stub bridge stands in for the
// concrete email.Bridge so we don't need a real IMAP/SMTP server.
func TestDaemonStartup_EmailEnabledHappy(t *testing.T) {
	tmpDir := t.TempDir()
	secretsPath := filepath.Join(tmpDir, "secrets", "email.json")
	writeEmailSecrets(t, secretsPath)

	secrets, err := config.LoadEmailSecrets(secretsPath, true /* enabled */)
	if err != nil {
		t.Fatalf("LoadEmailSecrets: %v", err)
	}
	if secrets == nil {
		t.Fatal("LoadEmailSecrets returned nil secrets with enabled=true")
	}

	// Construct the bridge (not Run()-ing it — tests don't need a real conn).
	emailCfg := config.EmailConfig{
		Enabled:      true,
		DaemonHandle: "test-handle",
		IMAP:         config.EmailIMAP{Host: "imap.example.com"},
		SMTP:         config.EmailSMTP{Host: "smtp.example.com"},
	}
	bridge := emailbridge.New(emailCfg, secrets, "18080")
	db := openEmailTestDB(t)
	bridge.SetDB(db)

	// Pass nil DB to the handler so requireAgentRegistered skips the agents
	// table check (happy-path goal is verifying bridge wiring, not auth).
	socketPath := startServerWithEmailHandlers(t, bridge, nil)

	// Verify email.status is reachable (non-404).
	result, errMsg := callMethod(t, socketPath, "email.status", map[string]any{
		"caller_agent_id": "user:test",
	})
	if errMsg != "" {
		t.Fatalf("email.status returned error: %s", errMsg)
	}
	if result == nil || bytes.Equal(result, []byte("null")) {
		t.Fatal("email.status returned nil/null result")
	}
}

// TestDaemonStartup_EmailRpcRoutedCorrectly verifies that all 7 email.* RPC
// methods are registered and routed to the email handler — i.e. the server
// returns any error code OTHER than JSON-RPC -32601 "Method not found".
// A handler error (auth, bridge_disabled, nil component) proves routing worked.
//
// Strategy:
//   - email.status and email.peer.list are safe with a nil-component stub
//     (handlers guard against nil bridge before accessing sub-components).
//   - The remaining five methods require bridge sub-components (Queue/Mesh/
//     Limiter) that are nil in the stub, so they would panic if called past
//     the bridge-enabled check. We pass Enabled=false so requireBridgeEnabled()
//     returns an application error (-32000) before any sub-component access —
//     proving the route is registered without triggering a nil-deref panic.
func TestDaemonStartup_EmailRpcRoutedCorrectly(t *testing.T) {
	// methodNotFoundCode is the JSON-RPC 2.0 error code for unregistered methods.
	const methodNotFoundCode = -32601

	// Enabled=false: requireBridgeEnabled fires for write methods, returning
	// "bridge_disabled" before any Queue/Mesh/Limiter access.
	bridge := &stubEmailBridge{
		cfg: config.EmailConfig{Enabled: false},
	}
	// nil DB: requireAgentRegistered / requireCoordinatorOrUser bypass.
	socketPath := startServerWithEmailHandlers(t, bridge, nil)

	methods := []struct {
		method string
		params any
	}{
		{"email.status", map[string]any{"caller_agent_id": "user:x"}},
		{"email.peer.list", map[string]any{"caller_agent_id": "user:x"}},
		{"email.send", map[string]any{"caller_agent_id": "user:x", "to_address": "a@b.com", "body": "hi"}},
		{"email.peer.pair", map[string]any{"caller_agent_id": "user:x", "to_handle": "h"}},
		{"email.peer.revoke", map[string]any{"caller_agent_id": "user:x", "to_handle": "h"}},
		{"email.peer.rebind", map[string]any{"caller_agent_id": "user:x", "to_handle": "h", "new_daemon_id": "d"}},
		{"email.unblock", map[string]any{"caller_agent_id": "user:x", "peer_key": "k"}},
	}

	for _, tc := range methods {
		_, errMsg, errCode := callMethodFull(t, socketPath, tc.method, tc.params)
		if errCode == methodNotFoundCode {
			t.Errorf("method %s not registered (got -32601 Method not found): %s", tc.method, errMsg)
		}
		// Non-zero errCode that is NOT -32601 means the handler ran and decided
		// (e.g. bridge_disabled, empty caller_agent_id). That is correct routing.
	}

	// Sanity check: an unknown method DOES return -32601.
	_, _, code := callMethodFull(t, socketPath, "email.nonexistent", map[string]any{})
	if code != methodNotFoundCode {
		t.Errorf("expected -32601 for unregistered method, got %d", code)
	}
}

// TestDaemon_EmailSendTelemetryAttribution verifies that a successful
// email.send call emits a slog record containing the from_agent field.
//
// The slog telemetry is emitted in HandleSend after Enqueue succeeds (D-B1.17
// step 5). We verify indirectly: the call must succeed (queueID > 0,
// status="queued") and the queue row must carry from_agent. The slog record
// itself is tested via log-capture in a dedicated rpc package test; here we
// confirm the handler-level contract so daemon startup is covered end-to-end.
func TestDaemon_EmailSendTelemetryAttribution(t *testing.T) {
	db := openEmailTestDB(t)

	// Register a test agent so requireAgentRegistered passes.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO agents (agent_id, kind, role, module, display, registered_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"agent:telemetry-test", "agent", "coordinator", "test", "Telemetry Test", now, now,
	)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}

	emailCfg := config.EmailConfig{
		Enabled:      true,
		DaemonHandle: "telem-handle",
		SMTP:         config.EmailSMTP{Host: "smtp.example.com"},
	}
	// Use a stub bridge that has a real Queue backed by our in-memory DB.
	queue := emailbridge.NewQueue(db)
	bridge := &stubEmailBridge{
		cfg:   emailCfg,
		queue: queue,
	}

	socketPath := startServerWithEmailHandlers(t, bridge, db)

	result, errMsg := callMethod(t, socketPath, "email.send", map[string]any{
		"caller_agent_id": "agent:telemetry-test",
		"to_address":      "peer@example.com",
		"subject":         "hello",
		"body":            "world",
	})
	if errMsg != "" {
		t.Fatalf("email.send failed: %s", errMsg)
	}

	var resp struct {
		Status    string `json:"status"`
		QueueID   int64  `json:"queue_id"`
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "queued" {
		t.Errorf("expected status=queued, got %s", resp.Status)
	}
	if resp.QueueID <= 0 {
		t.Errorf("expected positive queue_id, got %d", resp.QueueID)
	}

	// Verify from_agent persisted in the queue row (the slog record mirrors
	// this value — from_agent is the canonical telemetry attribution field).
	var fromAgent string
	err = db.QueryRow(
		`SELECT from_agent FROM email_outbound_queue WHERE id = ?`, resp.QueueID,
	).Scan(&fromAgent)
	if err != nil {
		t.Fatalf("query queue row: %v", err)
	}
	if fromAgent != "agent:telemetry-test" {
		t.Errorf("expected from_agent=agent:telemetry-test, got %q", fromAgent)
	}
}

// TestDaemon_EmailReloadCancelsAndRestarts is skipped: the daemon does not
// currently support config-reload for any bridge (telegram included). Adding
// it for email would be non-trivial and is deferred to a follow-up. The
// Restart() method on *email.Bridge exists and is tested in the bridge package.
func TestDaemon_EmailReloadCancelsAndRestarts(t *testing.T) {
	t.Skip("config-reload not yet supported by the daemon for any bridge (follows telegram's pattern of no hot-reload); tested at bridge unit level")
}

// TestDaemon_EmailShutdownCleanExit verifies that a daemon started with the
// email bridge running exits cleanly when the context is cancelled. The bridge
// goroutine must respect ctx cancellation and exit within 5 seconds.
func TestDaemon_EmailShutdownCleanExit(t *testing.T) {
	tmpDir := t.TempDir()
	secretsPath := filepath.Join(tmpDir, "secrets", "email.json")
	writeEmailSecrets(t, secretsPath)

	secrets, err := config.LoadEmailSecrets(secretsPath, true)
	if err != nil {
		t.Fatalf("LoadEmailSecrets: %v", err)
	}

	emailCfg := config.EmailConfig{
		Enabled:      true,
		DaemonHandle: "shutdown-test",
		IMAP:         config.EmailIMAP{Host: "127.0.0.1", Port: 1}, // non-routable → fails fast
		SMTP:         config.EmailSMTP{Host: "127.0.0.1", Port: 1},
	}

	db := openEmailTestDB(t)
	bridge := emailbridge.New(emailCfg, secrets, "18081")
	bridge.SetDB(db)
	// Compress retry backoff so the test doesn't wait 5s between retries.
	bridge.RetryBackoff = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	bridgeDone := make(chan struct{})
	go func() {
		bridge.Run(ctx)
		close(bridgeDone)
	}()

	// Give the bridge a moment to start its first inner run attempt
	// (which will fail immediately since the IMAP host is unreachable).
	time.Sleep(50 * time.Millisecond)

	// Cancel the context — this is what daemon shutdown does.
	cancel()

	select {
	case <-bridgeDone:
		// Bridge exited cleanly within the deadline.
	case <-time.After(5 * time.Second):
		t.Fatal("email bridge goroutine did not exit within 5s after context cancel")
	}
}
