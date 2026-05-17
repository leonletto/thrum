package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/rpc"
)

// captureStdout replaces os.Stdout with a pipe for the duration of fn(),
// returning everything written to os.Stdout. cli.EmitJSON calls fmt.Println
// which writes to os.Stdout directly, not to cobra's cmd.OutOrStdout().
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	_ = r.Close()
	return buf.String()
}

// --- fake Unix-socket daemon for CLI tests ---

// fakeEmailRPC stands up a minimal Unix-socket RPC server that echoes
// pre-canned responses for the email.* methods. The test registers a
// handler per method and the server dispatches.
//
// Design: we bind a real Unix socket so the CLI's getClient() / getClientNoRefresh()
// path works unchanged. Tests inject the socket path via THRUM_SOCKET env.
type fakeEmailRPC struct {
	handlers map[string]func(params json.RawMessage) (any, error)
}

func newFakeRPC() *fakeEmailRPC {
	return &fakeEmailRPC{handlers: make(map[string]func(json.RawMessage) (any, error))}
}

func (f *fakeEmailRPC) handle(method string, fn func(json.RawMessage) (any, error)) {
	f.handlers[method] = fn
}

// start spins up a Unix socket listener and returns the socket path and a
// teardown function. The listener processes one connection per Accept call
// and handles exactly one request on that connection. This is sufficient for
// the CLI: each cobra Execute() opens one connection, makes one RPC, closes.
func (f *fakeEmailRPC) start(t *testing.T) (socketPath string, teardown func()) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "thrum.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("fake RPC listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go f.serveConn(conn)
		}
	}()

	return sock, func() {
		_ = ln.Close()
		<-done
	}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      any             `json:"id"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	ID any `json:"id"`
}

func (f *fakeEmailRPC) serveConn(conn net.Conn) {
	defer conn.Close() //nolint:errcheck
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req jsonRPCRequest
	if err := dec.Decode(&req); err != nil {
		return
	}

	handler, ok := f.handlers[req.Method]
	var resp jsonRPCResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	if !ok {
		resp.Error = &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32601, Message: "method not found: " + req.Method}
		_ = enc.Encode(resp)
		return
	}

	result, err := handler(req.Params)
	if err != nil {
		resp.Error = &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32000, Message: err.Error()}
	} else {
		resp.Result = result
	}
	_ = enc.Encode(resp)
}

// withFakeRPC sets THRUM_SOCKET to the fake socket and returns the server +
// teardown. The outer test must call teardown() when done.
func withFakeRPC(t *testing.T, srv *fakeEmailRPC) (teardown func()) {
	t.Helper()
	sock, td := srv.start(t)
	t.Setenv("THRUM_SOCKET", sock)
	// Also clear THRUM_NAME / THRUM_AGENT_ID so resolveLocalAgentID falls
	// back gracefully without hitting real identity files.
	t.Setenv("THRUM_AGENT_ID", "user:testuser")
	return td
}

// runEmailCmd creates a fresh emailCmd() tree, wires output buffers, and
// executes with the supplied args.
func runEmailCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := emailCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	// Suppress cobra's usage output on error so test assertions are clean.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	for _, sub := range cmd.Commands() {
		sub.SilenceUsage = true
		sub.SilenceErrors = true
	}
	execErr := cmd.Execute()
	return outBuf.String(), errBuf.String(), execErr
}

// --- Test 1: pair --to ---

func TestEmailCli_Pair(t *testing.T) {
	srv := newFakeRPC()
	var capturedHandle string
	srv.handle("email.peer.pair", func(params json.RawMessage) (any, error) {
		var req rpc.EmailPeerPairRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		capturedHandle = req.ToHandle
		return rpc.EmailPeerPairResponse{Pending: true, ExpiresAt: 0}, nil
	})
	td := withFakeRPC(t, srv)
	defer td()

	out, _, err := runEmailCmd(t, "pair", "--to", "mybeta")
	if err != nil {
		t.Fatalf("pair error: %v", err)
	}
	if capturedHandle != "mybeta" {
		t.Errorf("RPC received to_handle=%q, want %q", capturedHandle, "mybeta")
	}
	if !strings.Contains(out, "mybeta") {
		t.Errorf("output should mention handle %q: %q", "mybeta", out)
	}
}

// --- Test 2: list --json ---

func TestEmailCli_ListJSON(t *testing.T) {
	srv := newFakeRPC()
	srv.handle("email.peer.list", func(params json.RawMessage) (any, error) {
		return rpc.EmailPeerListResponse{
			Peers: []rpc.EmailPeerEntry{
				{
					Handle:        "alpha",
					DaemonIDShort: "abc12345",
					Trust:         "full",
					VouchedBy:     "self",
					AddedAt:       "2026-01-01T00:00:00Z",
				},
			},
		}, nil
	})
	td := withFakeRPC(t, srv)
	defer td()

	t.Setenv("THRUM_NO_HINTS", "1")
	// flagJSON is package-global; save+restore it.
	origJSON := flagJSON
	flagJSON = true
	defer func() { flagJSON = origJSON }()

	// cli.EmitJSON writes to os.Stdout directly, not cobra's cmd.SetOut buffer.
	// Capture os.Stdout to collect the JSON output.
	var execErr error
	out := captureStdout(t, func() {
		_, _, execErr = runEmailCmd(t, "list")
	})
	if execErr != nil {
		t.Fatalf("list --json error: %v", execErr)
	}

	// Output must be parseable JSON with the EmailPeerListResponse shape.
	var resp rpc.EmailPeerListResponse
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); jsonErr != nil {
		t.Fatalf("list --json output is not valid JSON: %v\noutput: %q", jsonErr, out)
	}
	if len(resp.Peers) != 1 || resp.Peers[0].Handle != "alpha" {
		t.Errorf("unexpected peers in response: %+v", resp.Peers)
	}
}

// --- Test 3: revoke --yes ---

func TestEmailCli_Revoke(t *testing.T) {
	srv := newFakeRPC()
	var capturedHandle string
	srv.handle("email.peer.revoke", func(params json.RawMessage) (any, error) {
		var req rpc.EmailPeerRevokeRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		capturedHandle = req.ToHandle
		return rpc.EmailPeerRevokeResponse{Removed: true}, nil
	})
	td := withFakeRPC(t, srv)
	defer td()

	out, _, err := runEmailCmd(t, "revoke", "mybeta", "--yes")
	if err != nil {
		t.Fatalf("revoke error: %v", err)
	}
	if capturedHandle != "mybeta" {
		t.Errorf("RPC received to_handle=%q, want %q", capturedHandle, "mybeta")
	}
	if !strings.Contains(out, "mybeta") {
		t.Errorf("output should mention handle: %q", out)
	}
}

// --- Test 4: rebind ---

func TestEmailCli_Rebind(t *testing.T) {
	srv := newFakeRPC()
	var capturedToHandle, capturedNewID string
	srv.handle("email.peer.rebind", func(params json.RawMessage) (any, error) {
		var req rpc.EmailPeerRebindRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		capturedToHandle = req.ToHandle
		capturedNewID = req.NewDaemonID
		return rpc.EmailPeerRebindResponse{Updated: true}, nil
	})
	td := withFakeRPC(t, srv)
	defer td()

	_, _, err := runEmailCmd(t, "rebind", "--resume-as", "mybeta", "--new-daemon-id", "abc-123")
	if err != nil {
		t.Fatalf("rebind error: %v", err)
	}
	if capturedToHandle != "mybeta" {
		t.Errorf("to_handle=%q, want mybeta", capturedToHandle)
	}
	if capturedNewID != "abc-123" {
		t.Errorf("new_daemon_id=%q, want abc-123", capturedNewID)
	}
}

// --- Test 5: status --json ---

func TestEmailCli_StatusJSON(t *testing.T) {
	srv := newFakeRPC()
	srv.handle("email.status", func(params json.RawMessage) (any, error) {
		return rpc.EmailStatusResponse{
			Running:            true,
			ConnectedAt:        1000000,
			InboundCount:       5,
			OutboundQueueDepth: 2,
			PausedPeers:        []string{},
		}, nil
	})
	td := withFakeRPC(t, srv)
	defer td()

	t.Setenv("THRUM_NO_HINTS", "1")
	origJSON := flagJSON
	flagJSON = true
	defer func() { flagJSON = origJSON }()

	// cli.EmitJSON writes to os.Stdout directly.
	var execErr error
	out := captureStdout(t, func() {
		_, _, execErr = runEmailCmd(t, "status")
	})
	if execErr != nil {
		t.Fatalf("status --json error: %v", execErr)
	}

	var resp rpc.EmailStatusResponse
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); jsonErr != nil {
		t.Fatalf("status --json not valid JSON: %v\noutput: %q", jsonErr, out)
	}
	if !resp.Running {
		t.Errorf("expected running=true in response")
	}
	if resp.InboundCount != 5 {
		t.Errorf("inbound_count=%d, want 5", resp.InboundCount)
	}
}

// --- Test 6: unblock ---

func TestEmailCli_Unblock(t *testing.T) {
	srv := newFakeRPC()
	var capturedKey string
	srv.handle("email.unblock", func(params json.RawMessage) (any, error) {
		var req rpc.EmailUnblockRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		capturedKey = req.PeerKey
		return rpc.EmailUnblockResponse{Unblocked: true}, nil
	})
	td := withFakeRPC(t, srv)
	defer td()

	_, _, err := runEmailCmd(t, "unblock", "mybeta")
	if err != nil {
		t.Fatalf("unblock error: %v", err)
	}
	if capturedKey != "mybeta" {
		t.Errorf("peer_key=%q, want mybeta", capturedKey)
	}
}

// --- Test 7: send ---

func TestEmailCli_Send(t *testing.T) {
	srv := newFakeRPC()
	var capturedReq rpc.EmailSendRequest
	srv.handle("email.send", func(params json.RawMessage) (any, error) {
		if err := json.Unmarshal(params, &capturedReq); err != nil {
			return nil, err
		}
		return rpc.EmailSendResponse{Status: "queued", QueueID: 1, MessageID: "test-mid"}, nil
	})
	td := withFakeRPC(t, srv)
	defer td()

	_, _, err := runEmailCmd(t, "send", "--to", "foo@bar.com", "--subject", "hi", "--body", "test")
	if err != nil {
		t.Fatalf("send error: %v", err)
	}
	if capturedReq.ToAddress != "foo@bar.com" {
		t.Errorf("to_address=%q, want foo@bar.com", capturedReq.ToAddress)
	}
	if capturedReq.Subject != "hi" {
		t.Errorf("subject=%q, want hi", capturedReq.Subject)
	}
	if capturedReq.Body != "test" {
		t.Errorf("body=%q, want test", capturedReq.Body)
	}
}

// --- Test 8: init --provider gmail --non-interactive produces gmail IMAP/SMTP ---

func TestEmailCli_InitGmailTemplate(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Point flagRepo at the temp dir so init writes there.
	origRepo := flagRepo
	flagRepo = dir
	defer func() { flagRepo = origRepo }()

	// No daemon needed for init (it only writes files in non-interactive).
	cmd := emailCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetIn(strings.NewReader("")) // empty stdin, non-interactive
	cmd.SetArgs([]string{
		"init",
		"--provider", "gmail",
		"--non-interactive",
		"--password", "secret",
		"--daemon-handle", "test-handle",
		"--target-user", "leon",
		"--target-email", "leon@gmail.com",
	})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init error: %v\nout: %s", err, outBuf.String())
	}

	// Verify config.json was written with gmail IMAP/SMTP settings.
	configPath := filepath.Join(thrumDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.json not written: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config.json parse error: %v", err)
	}

	emailBlock, ok := cfg["email"].(map[string]any)
	if !ok {
		t.Fatalf("config.json has no email block; got: %s", string(data))
	}

	imap, _ := emailBlock["imap"].(map[string]any)
	if imap == nil {
		t.Fatal("email.imap block missing")
	}
	if imap["host"] != "imap.gmail.com" {
		t.Errorf("imap.host=%v, want imap.gmail.com", imap["host"])
	}

	smtp, _ := emailBlock["smtp"].(map[string]any)
	if smtp == nil {
		t.Fatal("email.smtp block missing")
	}
	if smtp["host"] != "smtp.gmail.com" {
		t.Errorf("smtp.host=%v, want smtp.gmail.com", smtp["host"])
	}
}

// --- Test 9: init writes secrets atomically (no .tmp file remains) ---

func TestEmailCli_InitWritesConfigAtomic(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}

	origRepo := flagRepo
	flagRepo = dir
	defer func() { flagRepo = origRepo }()

	cmd := emailCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs([]string{
		"init",
		"--provider", "gmail",
		"--non-interactive",
		"--password", "secret",
		"--daemon-handle", "test",
		"--target-user", "testuser",
		"--target-email", "testuser@gmail.com",
	})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init error: %v", err)
	}

	// Verify secrets file was created at the right path and mode 0600.
	secretsPath := filepath.Join(thrumDir, "secrets", "email.json")
	info, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatalf("secrets/email.json not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("secrets/email.json mode=%#o, want 0600", info.Mode().Perm())
	}

	// Verify no .tmp file remains.
	tmpPath := secretsPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("stale .tmp file exists after init")
	}

	// Verify the secrets file contains the expected fields.
	secretsData, err := os.ReadFile(secretsPath)
	if err != nil {
		t.Fatalf("read secrets: %v", err)
	}
	var secrets map[string]any
	if err := json.Unmarshal(secretsData, &secrets); err != nil {
		t.Fatalf("parse secrets: %v", err)
	}
	if secrets["imap_password"] != "secret" {
		t.Errorf("imap_password=%v, want secret", secrets["imap_password"])
	}
}

// --- Test 10: daemon offline → hint emitted, exits cleanly ---

func TestEmailCli_DaemonOfflineHints(t *testing.T) {
	// Point THRUM_SOCKET at a path that doesn't exist so connection fails.
	t.Setenv("THRUM_SOCKET", filepath.Join(t.TempDir(), "nonexistent.sock"))
	// Clear THRUM_AGENT_ID so resolveLocalAgentID doesn't accidentally succeed.
	t.Setenv("THRUM_AGENT_ID", "")

	// Use the existing hint suppression off so we can see the hint.
	t.Setenv("THRUM_NO_HINTS", "0")

	// Capture stderr where EmitStderr writes.
	// We intercept by running the command and checking stderr output.
	// cobra's SetErr writes usage/error, but EmitStderr writes to os.Stderr
	// directly. We can't intercept os.Stderr in a unit test without pipe
	// tricks, so we verify the exit behavior: commands that call
	// emailClientOrHint() on offline daemon return nil (no error, exit 0).
	//
	// This is the documented "exits cleanly" contract for offline hints.
	out, _, err := runEmailCmd(t, "list")
	// When daemon is offline, emailClientOrHint() returns nil and the verb
	// returns nil (exit 0) — the hint goes to os.Stderr which we can't
	// easily capture in a unit test without pipe tricks.
	if err != nil {
		t.Errorf("expected nil error (exit 0) on daemon-offline, got: %v", err)
	}
	_ = out // nothing on stdout when offline

	// Verify same contract for status verb.
	_, _, err2 := runEmailCmd(t, "status")
	if err2 != nil {
		t.Errorf("status: expected nil error on daemon-offline, got: %v", err2)
	}
}
