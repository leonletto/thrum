package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

// mockMonitorDaemon is a helper that sets up a mock daemon responding to a
// single monitor RPC call and returns a client connected to it.
// The handler func receives the raw decoded request and sends back the supplied
// response body as the "result" field.
type mockMonitorHandler struct {
	method         string // expected method name
	validateParams func(t *testing.T, params map[string]any)
	response       any
}

func setupMonitorDaemon(t *testing.T, h mockMonitorHandler) *Client {
	t.Helper()
	daemon, socketPath := newMockDaemon(t)
	t.Cleanup(daemon.stop)

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("mock: failed to decode request: %v", err)
			return
		}

		if h.method != "" && request["method"] != h.method {
			t.Errorf("expected method %q, got %v", h.method, request["method"])
		}

		if h.validateParams != nil {
			params, _ := request["params"].(map[string]any)
			h.validateParams(t, params)
		}

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  h.response,
		}
		if err := encoder.Encode(resp); err != nil {
			t.Logf("mock: failed to encode response: %v", err)
		}
	})

	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// ---------------------------------------------------------------------------
// TestMonitorAdd_TokenizesArgvFromDoubleDash
//
// This test simulates the cobra layer passing post-'--' tokens to MonitorStart.
// It asserts that the argv forwarded in the RPC request is exactly the slice
// provided — no shell tokenization is performed by the CLI layer.
// ---------------------------------------------------------------------------

func TestMonitorArgvFromDash(t *testing.T) {
	// Post-'--' args as cobra would supply them after splitting on '--'.
	// Simulating: thrum monitor add --name x --match y --to @t -- /bin/foo --flag1 arg1
	postDashArgs := []string{"/bin/foo", "--flag1", "arg1"}

	var capturedArgv []string

	client := setupMonitorDaemon(t, mockMonitorHandler{
		method: "monitor.start",
		validateParams: func(t *testing.T, params map[string]any) {
			t.Helper()
			rawArgv, ok := params["argv"].([]any)
			if !ok {
				t.Fatalf("params[argv] is not a slice, got %T: %v", params["argv"], params["argv"])
			}
			capturedArgv = make([]string, len(rawArgv))
			for i, v := range rawArgv {
				capturedArgv[i], _ = v.(string)
			}
		},
		response: map[string]string{"id": "mon_TEST001"},
	})

	req := MonitorStartRequest{
		Name:   "x",
		Match:  "y",
		Target: "@t",
		Argv:   postDashArgs, // argv comes directly from post-'--' cobra args
		Cwd:    "/tmp",
		Env:    map[string]string{},
	}

	result, err := MonitorStart(client, req)
	if err != nil {
		t.Fatalf("MonitorStart: %v", err)
	}
	if result.ID != "mon_TEST001" {
		t.Errorf("expected id mon_TEST001, got %s", result.ID)
	}

	// Assert argv slice is forwarded exactly — no shell splitting.
	if len(capturedArgv) != len(postDashArgs) {
		t.Fatalf("expected argv len %d, got %d: %v", len(postDashArgs), len(capturedArgv), capturedArgv)
	}
	for i, want := range postDashArgs {
		if capturedArgv[i] != want {
			t.Errorf("argv[%d]: want %q, got %q", i, want, capturedArgv[i])
		}
	}
}

// ---------------------------------------------------------------------------
// TestMonitorShow_DisplaysRedactedEnv
//
// Security-critical test.  The daemon already redacts env values before sending
// them over the wire.  The CLI must:
//   1. Accept the daemon's already-redacted env map.
//   2. Render each key as KEY=<redacted> — never the value from the wire.
//   3. MUST NOT contain the literal string "supersecretvalue" anywhere in output.
//
// Assertion 3 is belt-and-braces: even if a future code path bypasses daemon
// redaction, the CLI rendering loop must not leak secret values.
// ---------------------------------------------------------------------------

func TestMonitorShowRedactedEnv(t *testing.T) {
	// Valid 26-char ULID-shape ID so MonitorShow short-circuits past the
	// name-resolution lookup (which would otherwise need a monitor.list mock
	// in this single-handler setup).
	const monID = "mon_01KR70C8NTMMTPNKFJMH8G5C07"

	// The daemon returns env values already redacted.
	client := setupMonitorDaemon(t, mockMonitorHandler{
		method: "monitor.show",
		response: map[string]any{
			"id":               monID,
			"name":             "secret-test",
			"argv":             []string{"echo", "test"},
			"match":            ".",
			"target":           "@x",
			"cwd":              "/tmp",
			"debounce_seconds": 60,
			"status":           "running",
			"created_at":       "2026-04-11T00:00:00Z",
			"updated_at":       "2026-04-11T00:00:00Z",
			// Daemon has already replaced raw values with "<redacted>".
			"env": map[string]any{
				"API_KEY":     "<redacted>",
				"DB_PASSWORD": "<redacted>",
			},
		},
	})

	var buf bytes.Buffer
	if err := MonitorShow(client, monID, &buf); err != nil {
		t.Fatalf("MonitorShow: %v", err)
	}

	output := buf.String()

	// 1. Keys must appear.
	if !strings.Contains(output, "API_KEY=<redacted>") {
		t.Errorf("output should contain API_KEY=<redacted>\nGot:\n%s", output)
	}
	if !strings.Contains(output, "DB_PASSWORD=<redacted>") {
		t.Errorf("output should contain DB_PASSWORD=<redacted>\nGot:\n%s", output)
	}

	// 2. Belt-and-braces: the literal raw secret value must never appear —
	//    even though the mock above didn't inject it, this assertion catches any
	//    future regression where the CLI might render env values from a source
	//    that bypassed daemon redaction.
	if strings.Contains(output, "supersecretvalue") {
		t.Errorf("SECURITY: output must not contain literal secret value 'supersecretvalue'\nGot:\n%s", output)
	}
}

// ---------------------------------------------------------------------------
// TestMonitorList_Empty / TestMonitorList_WithEntries
// ---------------------------------------------------------------------------

func TestMonitorList_Empty(t *testing.T) {
	client := setupMonitorDaemon(t, mockMonitorHandler{
		method:   "monitor.list",
		response: []any{},
	})

	var buf bytes.Buffer
	if err := MonitorList(client, false, &buf); err != nil {
		t.Fatalf("MonitorList: %v", err)
	}
	if !strings.Contains(buf.String(), "No monitors") {
		t.Errorf("expected 'No monitors' in output, got: %s", buf.String())
	}
}

func TestMonitorList_WithEntries(t *testing.T) {
	client := setupMonitorDaemon(t, mockMonitorHandler{
		method: "monitor.list",
		response: []map[string]any{
			{
				"id": "mon_001", "name": "dev-errors", "status": "running",
				"target": "@team", "match": "ERROR", "argv": []string{"tail", "-F", "/tmp/a.log"},
				"cwd": "/tmp", "debounce_seconds": 60, "env": map[string]any{},
				"created_at": "2026-04-11T00:00:00Z", "updated_at": "2026-04-11T00:00:00Z",
			},
		},
	})

	var buf bytes.Buffer
	if err := MonitorList(client, false, &buf); err != nil {
		t.Fatalf("MonitorList: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "mon_001") {
		t.Errorf("expected mon_001 in output, got: %s", output)
	}
	if !strings.Contains(output, "dev-errors") {
		t.Errorf("expected dev-errors in output, got: %s", output)
	}
	if !strings.Contains(output, "running") {
		t.Errorf("expected running status in output, got: %s", output)
	}
	// R2.3: UPTIME and PID columns must be present in the header row.
	if !strings.Contains(output, "UPTIME") || !strings.Contains(output, "PID") {
		t.Errorf("expected UPTIME and PID columns in header, got:\n%s", output)
	}
}

// ---------------------------------------------------------------------------
// TestMonitorStop
// ---------------------------------------------------------------------------

func TestMonitorStop_SendsIDParam(t *testing.T) {
	const monID = "mon_01KR70C8NTMMTPNKFJMH8G5C07" // valid 26-char ULID suffix

	var capturedID string
	client := setupMonitorDaemon(t, mockMonitorHandler{
		method: "monitor.stop",
		validateParams: func(t *testing.T, params map[string]any) {
			t.Helper()
			capturedID, _ = params["id"].(string)
		},
		response: map[string]string{"status": "stopped"},
	})

	if _, err := MonitorStop(client, monID); err != nil {
		t.Fatalf("MonitorStop: %v", err)
	}
	if capturedID != monID {
		t.Errorf("expected id %s, got %q", monID, capturedID)
	}
}

// ---------------------------------------------------------------------------
// TestMonitorRestart
// ---------------------------------------------------------------------------

// TestMonitorRestart_PreservesIDOnIDInput verifies that supplying a valid
// monitor ID short-circuits past the name lookup and the daemon's restart
// response is consumed without error. HandleRestart preserves the ID, so
// MonitorRestart should echo that same ID back to the caller.
func TestMonitorRestartNewID(t *testing.T) {
	const monID = "mon_01KR70C8NTMMTPNKFJMH8G5C20"

	client := setupMonitorDaemon(t, mockMonitorHandler{
		method:   "monitor.restart",
		response: map[string]string{"id": monID},
	})

	resolvedID, err := MonitorRestart(client, monID)
	if err != nil {
		t.Fatalf("MonitorRestart: %v", err)
	}
	if resolvedID != monID {
		t.Errorf("expected resolvedID to echo input %s, got %s", monID, resolvedID)
	}
}

// ---------------------------------------------------------------------------
// TestMonitorStart_EnvMapIsForwarded
// ---------------------------------------------------------------------------

func TestMonitorStartEnvForwarded(t *testing.T) {
	var capturedEnv map[string]any

	client := setupMonitorDaemon(t, mockMonitorHandler{
		method: "monitor.start",
		validateParams: func(t *testing.T, params map[string]any) {
			t.Helper()
			capturedEnv, _ = params["env"].(map[string]any)
		},
		response: map[string]string{"id": "mon_ENV001"},
	})

	req := MonitorStartRequest{
		Name:   "env-test",
		Match:  ".",
		Target: "@x",
		Argv:   []string{"echo", "hello"},
		Cwd:    "/tmp",
		Env:    map[string]string{"MY_KEY": "my-value"},
	}

	if _, err := MonitorStart(client, req); err != nil {
		t.Fatalf("MonitorStart: %v", err)
	}

	if capturedEnv["MY_KEY"] != "my-value" {
		t.Errorf("expected env[MY_KEY]=my-value, got %v", capturedEnv["MY_KEY"])
	}
}

// ---------------------------------------------------------------------------
// Name-resolution tests (thrum-puhr.9.1) — `thrum monitor stop <name>` and
// `thrum monitor logs <name>` must resolve names to IDs at the CLI surface
// before dispatching the RPC.
//
// Implementation contract (see resolveMonitorIdentifier in monitor.go):
//   - Identifier with "mon_" prefix → passed through to RPC unchanged
//     (no monitor.list round-trip required).
//   - Identifier without "mon_" prefix → monitor.list called first, name
//     matched against returned MonitorJobView.Name, resolved ID forwarded.
//   - Stop uses include_all=false (running only); logs uses include_all=true
//     (historical lookup includes stopped/dead within 1wk).
//   - Unknown name → clean error referencing the user-typed identifier and
//     suggesting `thrum monitor list` to discover available monitors.
//
// Mock-daemon-level guarantees we assert here (vs. cobra-layer integration):
//   - Resolution happens before monitor.stop / monitor.logs is invoked.
//   - The resolved ID (NOT the name) appears in the RPC params.
//   - Pre-resolved IDs short-circuit the lookup (no monitor.list round-trip).
// ---------------------------------------------------------------------------

// setupMonitorDaemonSequence sets up a mock daemon that handles a sequence of
// JSON-RPC requests on a single connection (the cli.Client reuses one conn
// across calls). Each handler in the sequence handles exactly one request.
// After the last handler the conn closes; if the client sends more requests
// than handlers, the extra ones surface as connection errors and fail the
// test naturally.
func setupMonitorDaemonSequence(t *testing.T, handlers ...mockMonitorHandler) *Client {
	t.Helper()
	daemon, socketPath := newMockDaemon(t)
	t.Cleanup(daemon.stop)

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		for i, h := range handlers {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				t.Logf("mock seq[%d]: decode failed: %v", i, err)
				return
			}

			if h.method != "" && request["method"] != h.method {
				t.Errorf("seq[%d]: expected method %q, got %v", i, h.method, request["method"])
			}

			if h.validateParams != nil {
				params, _ := request["params"].(map[string]any)
				h.validateParams(t, params)
			}

			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result":  h.response,
			}
			if err := encoder.Encode(resp); err != nil {
				t.Logf("mock seq[%d]: encode failed: %v", i, err)
				return
			}
		}
	})

	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// monitorListResponse builds a monitor.list response payload from a slice of
// (id, name, status) triples. Only the fields that name-resolution reads are
// populated; the rest are zeroed out (the JSON decoder tolerates missing
// fields against the MonitorJobView struct tags).
func monitorListResponse(monitors ...[3]string) []map[string]any {
	out := make([]map[string]any, 0, len(monitors))
	for _, m := range monitors {
		out = append(out, map[string]any{
			"id":               m[0],
			"name":             m[1],
			"status":           m[2],
			"argv":             []string{},
			"match":            "",
			"target":           "",
			"cwd":              "",
			"env":              map[string]any{},
			"debounce_seconds": 0,
			"created_at":       "2026-05-19T00:00:00Z",
			"updated_at":       "2026-05-19T00:00:00Z",
		})
	}
	return out
}

// Valid 26-char-suffix monitor IDs used across the name-resolution tests.
// Anything shorter or with lowercase / dash chars would fail isMonitorID and
// route through the name-lookup path, which would invalidate the "accept ID"
// tests' assertion that no monitor.list round-trip occurs.
const (
	testMonitorIDDaily    = "mon_01KR70C8NTMMTPNKFJMH8G5C07"
	testMonitorIDOther    = "mon_01KR70C8NTMMTPNKFJMH8G5C08"
	testMonitorIDLogs     = "mon_01KR70C8NTMMTPNKFJMH8G5C09"
	testMonitorIDHistoric = "mon_01KR70C8NTMMTPNKFJMH8G5C0A"
)

func TestMonitorStop_AcceptsID(t *testing.T) {
	var capturedID string
	client := setupMonitorDaemonSequence(t, mockMonitorHandler{
		method: "monitor.stop",
		validateParams: func(t *testing.T, params map[string]any) {
			t.Helper()
			capturedID, _ = params["id"].(string)
		},
		response: map[string]string{"status": "stopped"},
	})

	resolvedID, err := MonitorStop(client, testMonitorIDDaily)
	if err != nil {
		t.Fatalf("MonitorStop: %v", err)
	}
	if capturedID != testMonitorIDDaily {
		t.Errorf("expected RPC to receive id %s, got %q", testMonitorIDDaily, capturedID)
	}
	if resolvedID != testMonitorIDDaily {
		t.Errorf("expected returned id %s, got %q", testMonitorIDDaily, resolvedID)
	}
}

func TestMonitorStop_ResolvesName(t *testing.T) {
	var listIncludeAll any
	var capturedStopID string

	client := setupMonitorDaemonSequence(t,
		mockMonitorHandler{
			method: "monitor.list",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				listIncludeAll = params["include_all"]
			},
			response: monitorListResponse(
				[3]string{testMonitorIDDaily, "daily-backup", "running"},
				[3]string{testMonitorIDOther, "other-monitor", "running"},
			),
		},
		mockMonitorHandler{
			method: "monitor.stop",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				capturedStopID, _ = params["id"].(string)
			},
			response: map[string]string{"status": "stopped"},
		},
	)

	resolvedID, err := MonitorStop(client, "daily-backup")
	if err != nil {
		t.Fatalf("MonitorStop(name): %v", err)
	}

	// Stop semantics: resolution must NOT include stopped/dead monitors.
	// JSON unmarshal of an absent `include_all` field yields nil, which the
	// daemon treats as false (matching the include_all,omitempty wire tag).
	if listIncludeAll != nil && listIncludeAll != false {
		t.Errorf("stop: monitor.list include_all should be false/absent, got %v", listIncludeAll)
	}
	if capturedStopID != testMonitorIDDaily {
		t.Errorf("expected stop RPC to receive resolved id %s, got %q", testMonitorIDDaily, capturedStopID)
	}
	if resolvedID != testMonitorIDDaily {
		t.Errorf("expected MonitorStop to return resolved id %s, got %q", testMonitorIDDaily, resolvedID)
	}
}

func TestMonitorStop_NameNotFound(t *testing.T) {
	client := setupMonitorDaemonSequence(t, mockMonitorHandler{
		method: "monitor.list",
		response: monitorListResponse(
			[3]string{testMonitorIDOther, "other-monitor", "running"},
		),
	})

	_, err := MonitorStop(client, "missing-name")
	if err == nil {
		t.Fatal("expected error for unknown name, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing-name") {
		t.Errorf("error should reference the typed name; got %q", msg)
	}
	if !strings.Contains(msg, "running monitor") {
		t.Errorf("stop should hint at running-only scope; got %q", msg)
	}
	if !strings.Contains(msg, "thrum monitor list") {
		t.Errorf("error should suggest `thrum monitor list`; got %q", msg)
	}
}

// TestMonitorStop_MonPrefixedName defends against the false-positive case
// where a user-typed name happens to begin with "mon_". Without the
// ULID-shape check in isMonitorID, "mon_daily" would be treated as a
// pre-resolved ID and sent straight to monitor.stop, which would return the
// original "-32000: monitor not found" cliff this fix is meant to remove.
// The sequence mock asserts that monitor.list is called first (resolution),
// then monitor.stop with the resolved ID — proving the lookup ran.
// (Test name kept short to stay under macOS's 104-char unix-socket path limit
// when combined with the t.TempDir() prefix under /var/folders/.../T.)
func TestMonitorStop_PrefixedName(t *testing.T) {
	const namedLikeID = "mon_daily" // shape-invalid: too short and has lowercase

	var capturedStopID string
	client := setupMonitorDaemonSequence(t,
		mockMonitorHandler{
			method: "monitor.list",
			response: monitorListResponse(
				[3]string{testMonitorIDDaily, namedLikeID, "running"},
			),
		},
		mockMonitorHandler{
			method: "monitor.stop",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				capturedStopID, _ = params["id"].(string)
			},
			response: map[string]string{"status": "stopped"},
		},
	)

	if _, err := MonitorStop(client, namedLikeID); err != nil {
		t.Fatalf("MonitorStop(mon_-prefixed name): %v", err)
	}
	if capturedStopID != testMonitorIDDaily {
		t.Errorf("expected resolved id %s in stop RPC (name lookup must run for shape-invalid mon_ inputs), got %q",
			testMonitorIDDaily, capturedStopID)
	}
}

func TestMonitorLogs_AcceptsID(t *testing.T) {
	var capturedID string
	client := setupMonitorDaemonSequence(t, mockMonitorHandler{
		method: "monitor.logs",
		validateParams: func(t *testing.T, params map[string]any) {
			t.Helper()
			capturedID, _ = params["id"].(string)
		},
		response: []any{},
	})

	var buf bytes.Buffer
	if err := MonitorLogs(client, testMonitorIDLogs, 10, &buf); err != nil {
		t.Fatalf("MonitorLogs: %v", err)
	}
	if capturedID != testMonitorIDLogs {
		t.Errorf("expected RPC to receive id %s, got %q", testMonitorIDLogs, capturedID)
	}
}

func TestMonitorLogs_ResolvesName(t *testing.T) {
	var listIncludeAll any
	var capturedLogsID string

	client := setupMonitorDaemonSequence(t,
		mockMonitorHandler{
			method: "monitor.list",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				listIncludeAll = params["include_all"]
			},
			response: monitorListResponse(
				[3]string{testMonitorIDHistoric, "old-job", "stopped"},
			),
		},
		mockMonitorHandler{
			method: "monitor.logs",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				capturedLogsID, _ = params["id"].(string)
			},
			response: []any{},
		},
	)

	var buf bytes.Buffer
	if err := MonitorLogs(client, "old-job", 10, &buf); err != nil {
		t.Fatalf("MonitorLogs(name): %v", err)
	}

	// Logs are historical — resolution MUST include stopped/dead.
	if listIncludeAll != true {
		t.Errorf("logs: monitor.list include_all should be true, got %v", listIncludeAll)
	}
	if capturedLogsID != testMonitorIDHistoric {
		t.Errorf("expected logs RPC to receive resolved id %s, got %q", testMonitorIDHistoric, capturedLogsID)
	}
}

func TestMonitorLogs_NameNotFound(t *testing.T) {
	client := setupMonitorDaemonSequence(t, mockMonitorHandler{
		method:   "monitor.list",
		response: monitorListResponse(),
	})

	var buf bytes.Buffer
	err := MonitorLogs(client, "ghost", 10, &buf)
	if err == nil {
		t.Fatal("expected error for unknown name, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ghost") {
		t.Errorf("error should reference the typed name; got %q", msg)
	}
	// Logs scope is "monitor" (not "running monitor") since include_all=true.
	if strings.Contains(msg, "running monitor") {
		t.Errorf("logs should NOT scope to running-only; got %q", msg)
	}
	if !strings.Contains(msg, "thrum monitor list --all") {
		t.Errorf("error should suggest `thrum monitor list --all`; got %q", msg)
	}
}

// ---------------------------------------------------------------------------
// Restart name-resolution tests (thrum-tv6z) — sibling of puhr.9.1; same
// CLI-layer name→ID resolution pattern applied to `thrum monitor restart`.
// Restart scopes to running monitors only (mirroring stop), since "restart"
// semantically operates on a live process.
// ---------------------------------------------------------------------------

func TestMonRestart_AcceptsID(t *testing.T) {
	var capturedID string
	client := setupMonitorDaemonSequence(t, mockMonitorHandler{
		method: "monitor.restart",
		validateParams: func(t *testing.T, params map[string]any) {
			t.Helper()
			capturedID, _ = params["id"].(string)
		},
		response: map[string]string{"id": testMonitorIDDaily},
	})

	resolvedID, err := MonitorRestart(client, testMonitorIDDaily)
	if err != nil {
		t.Fatalf("MonitorRestart: %v", err)
	}
	if capturedID != testMonitorIDDaily {
		t.Errorf("expected RPC to receive id %s, got %q", testMonitorIDDaily, capturedID)
	}
	if resolvedID != testMonitorIDDaily {
		t.Errorf("expected resolvedID to echo ID input %s, got %q", testMonitorIDDaily, resolvedID)
	}
}

func TestMonRestart_ResolvesName(t *testing.T) {
	var listIncludeAll any
	var capturedRestartID string

	client := setupMonitorDaemonSequence(t,
		mockMonitorHandler{
			method: "monitor.list",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				listIncludeAll = params["include_all"]
			},
			response: monitorListResponse(
				[3]string{testMonitorIDDaily, "daily-backup", "running"},
			),
		},
		mockMonitorHandler{
			method: "monitor.restart",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				capturedRestartID, _ = params["id"].(string)
			},
			response: map[string]string{"id": testMonitorIDDaily},
		},
	)

	resolvedID, err := MonitorRestart(client, "daily-backup")
	if err != nil {
		t.Fatalf("MonitorRestart(name): %v", err)
	}

	// Restart semantics: resolution must NOT include stopped/dead (mirrors
	// stop). JSON unmarshal of an absent `include_all` field yields nil,
	// which the daemon treats as false (omitempty wire tag).
	if listIncludeAll != nil && listIncludeAll != false {
		t.Errorf("restart: monitor.list include_all should be false/absent, got %v", listIncludeAll)
	}
	if capturedRestartID != testMonitorIDDaily {
		t.Errorf("expected restart RPC to receive resolved id %s, got %q", testMonitorIDDaily, capturedRestartID)
	}
	if resolvedID != testMonitorIDDaily {
		t.Errorf("expected MonitorRestart to return resolved id %s, got %q", testMonitorIDDaily, resolvedID)
	}
}

func TestMonRestart_NameNotFound(t *testing.T) {
	client := setupMonitorDaemonSequence(t, mockMonitorHandler{
		method: "monitor.list",
		response: monitorListResponse(
			[3]string{testMonitorIDOther, "other-monitor", "running"},
		),
	})

	_, err := MonitorRestart(client, "missing-name")
	if err == nil {
		t.Fatal("expected error for unknown name, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing-name") {
		t.Errorf("error should reference the typed name; got %q", msg)
	}
	if !strings.Contains(msg, "running monitor") {
		t.Errorf("restart should hint at running-only scope; got %q", msg)
	}
	if !strings.Contains(msg, "thrum monitor list") {
		t.Errorf("error should suggest `thrum monitor list`; got %q", msg)
	}
}

// TestMonRestart_PrefixedName mirrors TestMonitorStop_PrefixedName: a
// user-typed name starting with "mon_" but failing the ULID-shape check must
// route through monitor.list, not be sent straight to the daemon as an ID.
// (Test name kept short to stay under macOS's 104-char unix-socket limit.)
func TestMonRestart_PrefixedName(t *testing.T) {
	const namedLikeID = "mon_nightly"

	var capturedID string
	client := setupMonitorDaemonSequence(t,
		mockMonitorHandler{
			method: "monitor.list",
			response: monitorListResponse(
				[3]string{testMonitorIDDaily, namedLikeID, "running"},
			),
		},
		mockMonitorHandler{
			method: "monitor.restart",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				capturedID, _ = params["id"].(string)
			},
			response: map[string]string{"id": testMonitorIDDaily},
		},
	)

	if _, err := MonitorRestart(client, namedLikeID); err != nil {
		t.Fatalf("MonitorRestart(mon_-prefixed name): %v", err)
	}
	if capturedID != testMonitorIDDaily {
		t.Errorf("expected resolved id %s in restart RPC (name lookup must run for shape-invalid mon_ inputs), got %q",
			testMonitorIDDaily, capturedID)
	}
}

// ---------------------------------------------------------------------------
// Show name-resolution tests (thrum-09wl) — third sibling of puhr.9.1 and
// tv6z; same CLI-layer name→ID resolution pattern applied to `thrum monitor
// show`. Show is read-only inspection so resolution uses `includeAll=true`
// (matching logs, NOT stop/restart's running-only scope) — operators
// inspecting historical/stopped monitors by name shouldn't hit a filter.
// ---------------------------------------------------------------------------

// monitorShowResponse builds a monitor.show response payload with the given
// ID + name and zeroed-out fields elsewhere. Existing TestMonitorShowRedactedEnv
// covers the rendering details; these resolution tests only need to assert
// that the right ID reached the RPC, not re-validate rendering.
func monitorShowResponse(id, name, status string) map[string]any {
	return map[string]any{
		"id":               id,
		"name":             name,
		"argv":             []string{},
		"match":            "",
		"target":           "",
		"cwd":              "",
		"debounce_seconds": 0,
		"status":           status,
		"created_at":       "2026-05-20T00:00:00Z",
		"updated_at":       "2026-05-20T00:00:00Z",
		"env":              map[string]any{},
	}
}

func TestMonShow_AcceptsID(t *testing.T) {
	var capturedID string
	client := setupMonitorDaemonSequence(t, mockMonitorHandler{
		method: "monitor.show",
		validateParams: func(t *testing.T, params map[string]any) {
			t.Helper()
			capturedID, _ = params["id"].(string)
		},
		response: monitorShowResponse(testMonitorIDDaily, "daily-backup", "running"),
	})

	var buf bytes.Buffer
	if err := MonitorShow(client, testMonitorIDDaily, &buf); err != nil {
		t.Fatalf("MonitorShow: %v", err)
	}
	if capturedID != testMonitorIDDaily {
		t.Errorf("expected RPC to receive id %s (no list round-trip), got %q", testMonitorIDDaily, capturedID)
	}
}

func TestMonShow_ResolvesName(t *testing.T) {
	var listIncludeAll any
	var capturedShowID string

	client := setupMonitorDaemonSequence(t,
		mockMonitorHandler{
			method: "monitor.list",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				listIncludeAll = params["include_all"]
			},
			// Mix running + stopped to verify include_all=true scope.
			response: monitorListResponse(
				[3]string{testMonitorIDDaily, "daily-backup", "stopped"},
				[3]string{testMonitorIDOther, "other-monitor", "running"},
			),
		},
		mockMonitorHandler{
			method: "monitor.show",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				capturedShowID, _ = params["id"].(string)
			},
			response: monitorShowResponse(testMonitorIDDaily, "daily-backup", "stopped"),
		},
	)

	var buf bytes.Buffer
	if err := MonitorShow(client, "daily-backup", &buf); err != nil {
		t.Fatalf("MonitorShow(name): %v", err)
	}

	// Show is read-only inspection — resolution MUST include stopped/dead so
	// operators can inspect historical monitors by name.
	if listIncludeAll != true {
		t.Errorf("show: monitor.list include_all should be true, got %v", listIncludeAll)
	}
	if capturedShowID != testMonitorIDDaily {
		t.Errorf("expected show RPC to receive resolved id %s, got %q", testMonitorIDDaily, capturedShowID)
	}
}

func TestMonShow_NameNotFound(t *testing.T) {
	client := setupMonitorDaemonSequence(t, mockMonitorHandler{
		method:   "monitor.list",
		response: monitorListResponse(),
	})

	var buf bytes.Buffer
	err := MonitorShow(client, "missing-name", &buf)
	if err == nil {
		t.Fatal("expected error for unknown name, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing-name") {
		t.Errorf("error should reference the typed name; got %q", msg)
	}
	// Show scope is "monitor" (not "running monitor") since include_all=true.
	if strings.Contains(msg, "running monitor") {
		t.Errorf("show should NOT scope to running-only; got %q", msg)
	}
	if !strings.Contains(msg, "thrum monitor list --all") {
		t.Errorf("error should suggest `thrum monitor list --all`; got %q", msg)
	}
}

// TestMonShowJSON_ResolvesName covers the --json entry point. The resolution
// helper is shared with MonitorShow so name handling is structurally
// equivalent, but the JSON path is independently wired and worth a sanity
// check — if a future refactor accidentally drops the resolveMonitorIdentifier
// call from MonitorShowJSON, the text-path tests would still pass and this
// test would be the canary.
func TestMonShowJSON_ResolvesName(t *testing.T) {
	var capturedShowID string

	client := setupMonitorDaemonSequence(t,
		mockMonitorHandler{
			method: "monitor.list",
			response: monitorListResponse(
				[3]string{testMonitorIDDaily, "daily-backup", "running"},
			),
		},
		mockMonitorHandler{
			method: "monitor.show",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				capturedShowID, _ = params["id"].(string)
			},
			response: monitorShowResponse(testMonitorIDDaily, "daily-backup", "running"),
		},
	)

	job, err := MonitorShowJSON(client, "daily-backup")
	if err != nil {
		t.Fatalf("MonitorShowJSON(name): %v", err)
	}
	if capturedShowID != testMonitorIDDaily {
		t.Errorf("expected show RPC to receive resolved id %s, got %q", testMonitorIDDaily, capturedShowID)
	}
	if job.ID != testMonitorIDDaily {
		t.Errorf("expected returned view ID %s, got %q", testMonitorIDDaily, job.ID)
	}
}

// TestMonShow_PrefixedName mirrors the stop/restart prefix-regression guards:
// a user-typed name beginning with "mon_" but failing the ULID-shape check
// must route through monitor.list, not be sent straight to the daemon as an
// ID. (Short test name to fit macOS's 104-char unix-socket path limit.)
func TestMonShow_PrefixedName(t *testing.T) {
	const namedLikeID = "mon_archive" // lowercase + too short → shape-invalid

	var capturedID string
	client := setupMonitorDaemonSequence(t,
		mockMonitorHandler{
			method: "monitor.list",
			response: monitorListResponse(
				[3]string{testMonitorIDHistoric, namedLikeID, "stopped"},
			),
		},
		mockMonitorHandler{
			method: "monitor.show",
			validateParams: func(t *testing.T, params map[string]any) {
				t.Helper()
				capturedID, _ = params["id"].(string)
			},
			response: monitorShowResponse(testMonitorIDHistoric, namedLikeID, "stopped"),
		},
	)

	var buf bytes.Buffer
	if err := MonitorShow(client, namedLikeID, &buf); err != nil {
		t.Fatalf("MonitorShow(mon_-prefixed name): %v", err)
	}
	if capturedID != testMonitorIDHistoric {
		t.Errorf("expected resolved id %s in show RPC (name lookup must run for shape-invalid mon_ inputs), got %q",
			testMonitorIDHistoric, capturedID)
	}
}
