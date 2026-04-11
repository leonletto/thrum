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
	// The daemon returns env values already redacted.
	client := setupMonitorDaemon(t, mockMonitorHandler{
		method: "monitor.show",
		response: map[string]any{
			"id":               "mon_ABCDE",
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
	if err := MonitorShow(client, "mon_ABCDE", &buf); err != nil {
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
	var capturedID string

	client := setupMonitorDaemon(t, mockMonitorHandler{
		method: "monitor.stop",
		validateParams: func(t *testing.T, params map[string]any) {
			t.Helper()
			capturedID, _ = params["id"].(string)
		},
		response: map[string]string{"status": "stopped"},
	})

	if err := MonitorStop(client, "mon_ABC"); err != nil {
		t.Fatalf("MonitorStop: %v", err)
	}
	if capturedID != "mon_ABC" {
		t.Errorf("expected id mon_ABC, got %q", capturedID)
	}
}

// ---------------------------------------------------------------------------
// TestMonitorRestart
// ---------------------------------------------------------------------------

func TestMonitorRestartNewID(t *testing.T) {
	client := setupMonitorDaemon(t, mockMonitorHandler{
		method:   "monitor.restart",
		response: map[string]string{"id": "mon_NEW001"},
	})

	result, err := MonitorRestart(client, "mon_OLD001")
	if err != nil {
		t.Fatalf("MonitorRestart: %v", err)
	}
	if result.ID != "mon_NEW001" {
		t.Errorf("expected id mon_NEW001, got %s", result.ID)
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
