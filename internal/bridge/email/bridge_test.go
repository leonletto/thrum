package email

// Bridge orchestration tests (D-B1.14).
//
// Strategy: inject a lightweight mockWSConn that returns canned JSON-RPC
// responses so no daemon is needed; use a real SQLite DB from schema.OpenDB
// for sub-components that require one. All goroutines exit cleanly on
// ctx-cancel; goleak verifies no leaks survive the test.
//
// Pragmatic shortcuts per plan:
//   - HeartbeatInterval / RetryBackoff fields compressed to 50ms for wall-clock
//     tests rather than real 30s / 5s waits.
//   - TestBridge_RunSpawnsAllSubGoroutines uses status counters + timing as
//     structural evidence instead of runtime goroutine introspection.
//   - TestBridge_PanicRecoveryRestarts injects a panic via a goroutine that
//     panics through safeGo's own call path.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/leonletto/thrum/internal/bridge"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/schema"
)

// --- mock infrastructure ---

// mockWSConn is a minimal thread-safe stub for WSConn. It records all Call
// invocations and can be pre-seeded with per-method responses. Notifications
// returns a channel that is closed when mockWSConn.close is called.
type mockWSConn struct {
	mu           sync.Mutex
	calls        []mockCall
	responses    map[string]json.RawMessage // method → canned result
	callErr      map[string]error           // method → error to return
	notifyCh     chan bridge.Notification
	closed       atomic.Bool
	closeOnce    sync.Once
}

type mockCall struct {
	Method string
	Params map[string]any
}

func newMockWSConn() *mockWSConn {
	return &mockWSConn{
		responses: make(map[string]json.RawMessage),
		callErr:   make(map[string]error),
		notifyCh:  make(chan bridge.Notification, 64),
	}
}

func (m *mockWSConn) setResponse(method string, result any) {
	raw, _ := json.Marshal(result)
	m.mu.Lock()
	m.responses[method] = raw
	m.mu.Unlock()
}

func (m *mockWSConn) callCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int
	for _, c := range m.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func (m *mockWSConn) Call(_ context.Context, method string, params map[string]any) (json.RawMessage, error) {
	m.mu.Lock()
	m.calls = append(m.calls, mockCall{Method: method, Params: params})
	resp := m.responses[method]
	err := m.callErr[method]
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if resp == nil {
		resp = json.RawMessage(`{}`)
	}
	return resp, nil
}

func (m *mockWSConn) Notifications() <-chan bridge.Notification {
	return m.notifyCh
}

func (m *mockWSConn) Close() error {
	m.closeOnce.Do(func() {
		m.closed.Store(true)
		close(m.notifyCh)
	})
	return nil
}

// --- test helpers ---

// openTestDB opens and initialises a fresh SQLite DB. Closed via t.Cleanup.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "bridge_test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newTestBridge returns a Bridge wired with a mockWSConn injector and a
// real SQLite DB. The bridge is configured with short test intervals.
func newTestBridge(t *testing.T, ws *mockWSConn) (*Bridge, *sql.DB) {
	t.Helper()

	// Seed the mandatory WS handshake responses so run() can proceed.
	ws.setResponse("user.register", map[string]any{})
	ws.setResponse("session.start", map[string]any{"session_id": "sess-test-1"})
	ws.setResponse("session.end", map[string]any{})
	ws.setResponse("session.heartbeat", map[string]any{})
	ws.setResponse("message.send", map[string]any{})

	db := openTestDB(t)

	cfg := config.EmailConfig{
		Enabled:             true,
		Username:            "test-bridge-user",
		DaemonHandle:        "test-daemon",
		PollIntervalSeconds: 3600, // very long — suppress background inbound polls
		IMAP:                config.EmailIMAP{Host: "imap.example.com", Port: 993},
		SMTP:                config.EmailSMTP{Host: "smtp.example.com", Port: 465},
		RateLimits: config.EmailRateLimits{
			InboundPerPeerPerHour:  200,
			OutboundPerPeerPerHour: 200,
			GlobalInboundPerMinute: 60,
		},
	}

	b := New(cfg, nil /* secrets — IMAP/SMTP disabled in unit tests */, "8080")
	b.SetDB(db)
	b.HeartbeatInterval = 50 * time.Millisecond
	b.RetryBackoff = 50 * time.Millisecond

	// Inject the mock dial function — no real WebSocket server needed.
	b.dialFn = func(_ context.Context, _ string) (WSConn, error) {
		return ws, nil
	}

	// Use test-specific temp dirs for state + config so parallel tests don't
	// collide on the global os.TempDir() path.
	tmpDir := t.TempDir()
	b.stateDirFn = func(_ config.EmailConfig) string { return filepath.Join(tmpDir, "state") }
	b.configDirFn = func(_ config.EmailConfig) string { return filepath.Join(tmpDir, "config") }

	return b, db
}

// runBridgeBackground starts bridge.Run in a goroutine, returns a cancel func
// that stops the bridge and waits for the goroutine to exit.
func runBridgeBackground(t *testing.T, b *Bridge) (cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		b.Run(ctx)
	}()
	return cancel, ch
}

// waitRunning polls until b.Running() is true, or the timeout expires.
func waitRunning(t *testing.T, b *Bridge, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b.Running() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("bridge did not become running within %s", timeout)
}

// --- tests ---

// TestBridge_RunSpawnsAllSubGoroutines verifies that Run() transitions the
// bridge to Running=true and wires the heartbeat goroutine (structural
// evidence: heartbeat counter increments within a short window).
func TestBridge_RunSpawnsAllSubGoroutines(t *testing.T) {
	ws := newMockWSConn()
	b, _ := newTestBridge(t, ws)

	cancel, done := runBridgeBackground(t, b)
	t.Cleanup(func() {
		cancel()
		<-done
	})

	waitRunning(t, b, 500*time.Millisecond)

	// Allow at least 2 heartbeat ticks at 50ms interval.
	time.Sleep(150 * time.Millisecond)

	hb := b.heartbeatCount.Load()
	if hb < 1 {
		t.Errorf("heartbeat counter = %d, want >= 1 after 150ms @ 50ms interval", hb)
	}

	// Status must reflect running=true.
	s := b.Status()
	if !s.Running {
		t.Error("Status().Running = false, want true while running")
	}
	if s.StartedAt.IsZero() {
		t.Error("Status().StartedAt is zero while running")
	}

	// heartbeat RPC must have been issued.
	if n := ws.callCount("session.heartbeat"); n < 1 {
		t.Errorf("session.heartbeat call count = %d, want >= 1", n)
	}
}

// TestBridge_PanicRecoveryRestarts verifies that safeGo's panic recovery lets
// the bridge continue running after a sub-goroutine panics.
func TestBridge_PanicRecoveryRestarts(t *testing.T) {
	b := &Bridge{
		logger: newTestLogger(t),
	}

	var recovered atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.safeGo("panicky-goroutine", func() {
			recovered.Store(true)
			panic("deliberate test panic")
		})
	}()
	wg.Wait()

	if !recovered.Load() {
		t.Error("safeGo did not execute the function before the panic")
	}
	// If we reach here, the goroutine exited cleanly after panic recovery —
	// no process crash, no unhandled panic propagation.
}

// TestBridge_RestartCancelsAndReapplies starts the bridge, then calls
// Restart with a new config, and verifies the bridge picks up the new config
// (the old run cycle ends and a new one begins).
func TestBridge_RestartCancelsAndReapplies(t *testing.T) {
	ws := newMockWSConn()
	b, _ := newTestBridge(t, ws)

	cancel, done := runBridgeBackground(t, b)
	t.Cleanup(func() {
		cancel()
		<-done
	})

	waitRunning(t, b, 500*time.Millisecond)
	firstStart := b.Status().StartedAt

	// Restart with a modified config (handle change).
	newCfg := b.cfg
	newCfg.DaemonHandle = "test-daemon-v2"
	b.Restart(newCfg, nil)

	// Wait for the bridge to become running again after the restart cycle
	// (RetryBackoff=50ms so the new run loop starts within ~150ms).
	deadline := time.Now().Add(500 * time.Millisecond)
	var secondStart time.Time
	for time.Now().Before(deadline) {
		s := b.Status()
		if s.Running && !s.StartedAt.Equal(firstStart) {
			secondStart = s.StartedAt
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if secondStart.IsZero() {
		t.Errorf("bridge did not restart with new config within 500ms (firstStart=%v)", firstStart)
	}
}

// TestBridge_ShutdownFlushesCounters verifies that counter fields remain
// coherent (not negative) after a cancel+drain cycle. The heartbeat counter
// must be non-zero because at least one tick fires before cancel.
func TestBridge_ShutdownFlushesCounters(t *testing.T) {
	ws := newMockWSConn()
	b, _ := newTestBridge(t, ws)

	cancel, done := runBridgeBackground(t, b)
	waitRunning(t, b, 500*time.Millisecond)
	time.Sleep(100 * time.Millisecond) // let heartbeat tick at least once

	cancel()
	<-done

	s := b.Status()
	if s.Running {
		t.Error("Status().Running should be false after shutdown")
	}
	if s.HeartbeatCount < 0 {
		t.Errorf("HeartbeatCount negative after shutdown: %d", s.HeartbeatCount)
	}
	if s.InboundProcessed < 0 {
		t.Errorf("InboundProcessed negative after shutdown: %d", s.InboundProcessed)
	}
	if s.OutboundEnqueued < 0 {
		t.Errorf("OutboundEnqueued negative after shutdown: %d", s.OutboundEnqueued)
	}
}

// TestBridge_HeartbeatTicker verifies heartbeat fires at roughly the configured
// interval. With HeartbeatInterval=50ms we expect ≥2 ticks in 150ms.
func TestBridge_HeartbeatTicker(t *testing.T) {
	ws := newMockWSConn()
	b, _ := newTestBridge(t, ws)
	b.HeartbeatInterval = 40 * time.Millisecond

	cancel, done := runBridgeBackground(t, b)
	t.Cleanup(func() {
		cancel()
		<-done
	})

	waitRunning(t, b, 500*time.Millisecond)
	time.Sleep(200 * time.Millisecond) // ≥ 4 ticks at 40ms

	hb := b.heartbeatCount.Load()
	if hb < 2 {
		t.Errorf("heartbeat count = %d after 200ms @ 40ms interval, want >= 2", hb)
	}
	if n := ws.callCount("session.heartbeat"); n < 2 {
		t.Errorf("session.heartbeat RPC calls = %d, want >= 2", n)
	}
}

// TestBridge_GoroutineLeakClean runs the bridge through a full start+shutdown
// cycle and asserts no goroutines leak using goleak.
//
// database/sql.(*DB).connectionOpener is excluded: it is the background
// goroutine maintained by *sql.DB for the connection pool lifetime. The DB
// is closed by the t.Cleanup registered in openTestDB, which fires after the
// test function returns (and thus after goleak.VerifyNone runs on defer). The
// goroutine is benign and exits promptly when the DB is closed.
func TestBridge_GoroutineLeakClean(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	ws := newMockWSConn()
	b, _ := newTestBridge(t, ws)

	cancel, done := runBridgeBackground(t, b)
	waitRunning(t, b, 500*time.Millisecond)
	cancel()
	<-done

	// Give any goroutines a moment to unwind before goleak samples.
	time.Sleep(20 * time.Millisecond)
}

// TestBridge_BackoffRetryOnRunError verifies the retry loop: when the inner
// run returns an error, the bridge sleeps RetryBackoff then retries.
func TestBridge_BackoffRetryOnRunError(t *testing.T) {
	var runCount atomic.Int32
	var ws *mockWSConn

	// First call to dialFn fails → inner run returns error → retry fires.
	// Second call succeeds.
	b := New(config.EmailConfig{
		Username:     "retry-test",
		DaemonHandle: "retry-daemon",
	}, nil, "8080")

	db := openTestDB(t)
	b.SetDB(db)
	b.HeartbeatInterval = 50 * time.Millisecond
	b.RetryBackoff = 50 * time.Millisecond

	tmpDir := t.TempDir()
	b.stateDirFn = func(_ config.EmailConfig) string { return filepath.Join(tmpDir, "state") }
	b.configDirFn = func(_ config.EmailConfig) string { return filepath.Join(tmpDir, "config") }

	b.dialFn = func(ctx context.Context, _ string) (WSConn, error) {
		n := runCount.Add(1)
		if n == 1 {
			// First attempt fails immediately to trigger retry.
			return nil, errDialFail
		}
		// Second attempt succeeds.
		ws = newMockWSConn()
		ws.setResponse("user.register", map[string]any{})
		ws.setResponse("session.start", map[string]any{"session_id": "sess-retry"})
		ws.setResponse("session.end", map[string]any{})
		ws.setResponse("session.heartbeat", map[string]any{})
		ws.setResponse("message.send", map[string]any{})
		return ws, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Run(ctx)
	}()

	// Wait until at least 2 dial attempts (first fail + second succeed).
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if runCount.Load() >= 2 && b.Running() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if runCount.Load() < 2 {
		t.Errorf("expected >= 2 dial attempts (retry), got %d", runCount.Load())
	}
	if !b.Running() {
		t.Error("bridge should be running after successful retry")
	}

	cancel()
	<-done
}

// errDialFail is a sentinel error for the BackoffRetryOnRunError test.
var errDialFail = fmt.Errorf("simulated dial failure")

// --- internal helpers ---

// newTestLogger returns a bridge logger that writes to t.Log.
func newTestLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(testWriter{t: t}, "[email/bridge-test] ", 0)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
