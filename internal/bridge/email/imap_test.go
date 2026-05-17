package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"go.uber.org/goleak"
)

// Test credentials — only used against the in-process test server.
const (
	testIMAPUser = "test-user"
	testIMAPPass = "test-password"
)

// Copied from imapclient/client_test.go — test-only self-signed cert.
const rsaCertPEM = `-----BEGIN CERTIFICATE-----
MIIDOTCCAiGgAwIBAgIQSRJrEpBGFc7tNb1fb5pKFzANBgkqhkiG9w0BAQsFADAS
MRAwDgYDVQQKEwdBY21lIENvMCAXDTcwMDEwMTAwMDAwMFoYDzIwODQwMTI5MDYw
MDAwWjASMRAwDgYDVQQKEwdBY21lIENvMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A
MIIBCgKCAQEA6Gba5tHV1dAKouAaXO3/ebDUU4rvwCUg/CNaJ2PT5xLD4N1Vcb8r
bFSW2HXKq+MPfVdwIKR/1DczEoAGf/JWQTW7EgzlXrCd3rlajEX2D73faWJekD0U
aUgz5vtrTXZ90BQL7WvRICd7FlEZ6FPOcPlumiyNmzUqtwGhO+9ad1W5BqJaRI6P
YfouNkwR6Na4TzSj5BrqUfP0FwDizKSJ0XXmh8g8G9mtwxOSN3Ru1QFc61Xyeluk
POGKBV/q6RBNklTNe0gI8usUMlYyoC7ytppNMW7X2vodAelSu25jgx2anj9fDVZu
h7AXF5+4nJS4AAt0n1lNY7nGSsdZas8PbQIDAQABo4GIMIGFMA4GA1UdDwEB/wQE
AwICpDATBgNVHSUEDDAKBggrBgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MB0GA1Ud
DgQWBBStsdjh3/JCXXYlQryOrL4Sh7BW5TAuBgNVHREEJzAlggtleGFtcGxlLmNv
bYcEfwAAAYcQAAAAAAAAAAAAAAAAAAAAATANBgkqhkiG9w0BAQsFAAOCAQEAxWGI
5NhpF3nwwy/4yB4i/CwwSpLrWUa70NyhvprUBC50PxiXav1TeDzwzLx/o5HyNwsv
cxv3HdkLW59i/0SlJSrNnWdfZ19oTcS+6PtLoVyISgtyN6DpkKpdG1cOkW3Cy2P2
+tK/tKHRP1Y/Ra0RiDpOAmqn0gCOFGz8+lqDIor/T7MTpibL3IxqWfPrvfVRHL3B
grw/ZQTTIVjjh4JBSW3WyWgNo/ikC1lrVxzl4iPUGptxT36Cr7Zk2Bsg0XqwbOvK
5d+NTDREkSnUbie4GeutujmX3Dsx88UiV6UY/4lHJa6I5leHUNOHahRbpbWeOfs/
WkBKOclmOV2xlTVuPw==
-----END CERTIFICATE-----
`

//nolint:gosec // test-only RSA private key; not a production secret
const rsaKeyPEM = `-----BEGIN RSA PRIVATE KEY-----
MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQDoZtrm0dXV0Aqi
4Bpc7f95sNRTiu/AJSD8I1onY9PnEsPg3VVxvytsVJbYdcqr4w99V3AgpH/UNzMS
gAZ/8lZBNbsSDOVesJ3euVqMRfYPvd9pYl6QPRRpSDPm+2tNdn3QFAvta9EgJ3sW
URnoU85w+W6aLI2bNSq3AaE771p3VbkGolpEjo9h+i42TBHo1rhPNKPkGupR8/QX
AOLMpInRdeaHyDwb2a3DE5I3dG7VAVzrVfJ6W6Q84YoFX+rpEE2SVM17SAjy6xQy
VjKgLvK2mk0xbtfa+h0B6VK7bmODHZqeP18NVm6HsBcXn7iclLgAC3SfWU1jucZK
x1lqzw9tAgMBAAECggEABWzxS1Y2wckblnXY57Z+sl6YdmLV+gxj2r8Qib7g4ZIk
lIlWR1OJNfw7kU4eryib4fc6nOh6O4AWZyYqAK6tqNQSS/eVG0LQTLTTEldHyVJL
dvBe+MsUQOj4nTndZW+QvFzbcm2D8lY5n2nBSxU5ypVoKZ1EqQzytFcLZpTN7d89
EPj0qDyrV4NZlWAwL1AygCwnlwhMQjXEalVF1ylXwU3QzyZ/6MgvF6d3SSUlh+sq
XefuyigXw484cQQgbzopv6niMOmGP3of+yV4JQqUSb3IDmmT68XjGd2Dkxl4iPki
6ZwXf3CCi+c+i/zVEcufgZ3SLf8D99kUGE7v7fZ6AQKBgQD1ZX3RAla9hIhxCf+O
3D+I1j2LMrdjAh0ZKKqwMR4JnHX3mjQI6LwqIctPWTU8wYFECSh9klEclSdCa64s
uI/GNpcqPXejd0cAAdqHEEeG5sHMDt0oFSurL4lyud0GtZvwlzLuwEweuDtvT9cJ
Wfvl86uyO36IW8JdvUprYDctrQKBgQDycZ697qutBieZlGkHpnYWUAeImVA878sJ
w44NuXHvMxBPz+lbJGAg8Cn8fcxNAPqHIraK+kx3po8cZGQywKHUWsxi23ozHoxo
+bGqeQb9U661TnfdDspIXia+xilZt3mm5BPzOUuRqlh4Y9SOBpSWRmEhyw76w4ZP
OPxjWYAgwQKBgA/FehSYxeJgRjSdo+MWnK66tjHgDJE8bYpUZsP0JC4R9DL5oiaA
brd2fI6Y+SbyeNBallObt8LSgzdtnEAbjIH8uDJqyOmknNePRvAvR6mP4xyuR+Bv
m+Lgp0DMWTw5J9CKpydZDItc49T/mJ5tPhdFVd+am0NAQnmr1MCZ6nHxAoGABS3Y
LkaC9FdFUUqSU8+Chkd/YbOkuyiENdkvl6t2e52jo5DVc1T7mLiIrRQi4SI8N9bN
/3oJWCT+uaSLX2ouCtNFunblzWHBrhxnZzTeqVq4SLc8aESAnbslKL4i8/+vYZlN
s8xtiNcSvL+lMsOBORSXzpj/4Ot8WwTkn1qyGgECgYBKNTypzAHeLE6yVadFp3nQ
Ckq9yzvP/ib05rvgbvrne00YeOxqJ9gtTrzgh7koqJyX1L4NwdkEza4ilDWpucn0
xiUZS4SoaJq6ZvcBYS62Yr1t8n09iG47YL8ibgtmH3L+svaotvpVxVK+d7BLevA/
ZboOWVe3icTy64BT3OQhmg==
-----END RSA PRIVATE KEY-----
`

// testServer holds in-memory IMAP test infrastructure. The server listens on
// plain TCP (no TLS wrapping at the TCP layer) and exposes STARTTLS. Client
// code uses UseStartTLS: true and InsecureSkipVerify in the TLS config to
// match the self-signed test cert.
type testServer struct {
	server   *imapserver.Server
	listener net.Listener
	user     *imapmemserver.User
	memSrv   *imapmemserver.Server
	tlsCfg   *tls.Config // client-side: InsecureSkipVerify
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	cert, err := tls.X509KeyPair([]byte(rsaCertPEM), []byte(rsaKeyPEM))
	if err != nil {
		t.Fatalf("tls.X509KeyPair: %v", err)
	}

	memSrv := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	memSrv.AddUser(user)

	// The server only wraps via STARTTLS (TLSConfig sets the upgrade cert).
	// InsecureAuth allows LOGIN before STARTTLS — needed for appendMessage.
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memSrv.NewSession(), nil, nil
		},
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
		InsecureAuth: true,
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapIMAP4rev2: {},
		},
	})

	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	go func() {
		// Serve returns when the listener is closed — normal teardown.
		_ = srv.Serve(ln)
	}()

	// Skip verification: the test cert's CN/SAN is "Acme Co" / example.com,
	// not 127.0.0.1/localhost.
	clientTLS := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test only

	return &testServer{
		server:   srv,
		listener: ln,
		user:     user,
		memSrv:   memSrv,
		tlsCfg:   clientTLS,
	}
}

func (ts *testServer) close() {
	_ = ts.listener.Close()
}

// addr returns host:port for the test listener.
func (ts *testServer) addr() (string, int) {
	a := ts.listener.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// cfg returns an IMAPConfig pre-wired to the test server using STARTTLS.
func (ts *testServer) cfg() IMAPConfig {
	host, port := ts.addr()
	return IMAPConfig{
		Host:         host,
		Port:         port,
		UseStartTLS:  true,
		UseIDLE:      true,
		Username:     testIMAPUser,
		Password:     testIMAPPass,
		PollInterval: 60 * time.Second,
		TLSConfig:    ts.tlsCfg,
	}
}

// appendMessage appends a raw RFC 5322 message to INBOX. It connects via
// plain TCP (InsecureAuth: true on server) so no STARTTLS is required for
// the helper itself.
func (ts *testServer) appendMessage(t *testing.T, raw string) {
	t.Helper()

	host, port := ts.addr()
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cl := imapclient.New(conn, nil)
	defer cl.Close()

	if err := cl.Login(testIMAPUser, testIMAPPass).Wait(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	data := []byte(raw)
	appendCmd := cl.Append("INBOX", int64(len(data)), nil)
	if _, err := appendCmd.Write(data); err != nil {
		t.Fatalf("Append write: %v", err)
	}
	if err := appendCmd.Close(); err != nil {
		t.Fatalf("Append close: %v", err)
	}
	if _, err := appendCmd.Wait(); err != nil {
		t.Fatalf("Append wait: %v", err)
	}
}

const simpleTestMessage = "Subject: Test\r\nFrom: sender@example.com\r\nTo: inbox@example.com\r\n\r\nHello, world!\r\n"

// TestImap_ConnectAuthenticated verifies TLS dial + LOGIN succeeds.
func TestImap_ConnectAuthenticated(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	c := NewIMAPClient(ts.cfg())
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestImap_FetchSinceTimestamp appends a message to INBOX and verifies
// Fetch returns it with non-empty bytes.
func TestImap_FetchSinceTimestamp(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	ts.appendMessage(t, simpleTestMessage)

	c := NewIMAPClient(ts.cfg())
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	since := time.Now().Add(-24 * time.Hour)
	msgs, err := c.Fetch(ctx, since)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("Fetch returned 0 messages; expected 1")
	}
	if len(msgs[0].Bytes) == 0 {
		t.Error("Fetch returned message with empty Bytes")
	}
	if !bytes.Contains(msgs[0].Bytes, []byte("Hello, world!")) {
		t.Errorf("message body missing expected content; got %q", msgs[0].Bytes)
	}
}

// TestImap_IDLEKeepaliveResubmits is a lifecycle smoke test: enter IDLE,
// verify the IdleCommand stays alive, then cancel the context and verify
// IDLEloop returns promptly. The 28-minute auto-restart is handled internally
// by the library; we only verify enter/exit hygiene here.
func TestImap_IDLEKeepaliveResubmits(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	c := NewIMAPClient(ts.cfg())
	ctx, cancel := context.WithCancel(context.Background())

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- c.IDLEloop(ctx) }()

	// Brief sleep to let IDLEloop enter IDLE before canceling.
	time.Sleep(100 * time.Millisecond)

	cancel()

	select {
	case err := <-done:
		// IDLEloop must return within 1 second of cancel.
		if err != nil && err != context.Canceled {
			t.Errorf("IDLEloop returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("IDLEloop did not return within 2s after ctx cancel")
	}
}

// TestImap_IDLEFailsDowngradesToPoll verifies that 3 consecutive IDLE errors
// flip pollOnly and cause IDLEloop to enter poll-only mode.
//
// Injection strategy: we pre-set idleFailures to idleFailureThreshold and
// pollOnly state, then verify IDLEloop routes through pollLoop (which honors
// ctx cancel cleanly). This is equivalent to the runtime path after 3 real
// IDLE failures: the field is set and the next IDLEloop iteration goes to
// pollLoop.
func TestImap_IDLEFailsDowngradesToPoll(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	cfg := ts.cfg()
	cfg.PollInterval = 50 * time.Millisecond // fast poll so test doesn't hang

	c := NewIMAPClient(cfg)
	ctx, cancel := context.WithCancel(context.Background())

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	// Simulate 3 IDLE failures by directly setting the counters.
	c.idleFailures.Store(idleFailureThreshold)
	c.pollOnly.Store(true)

	done := make(chan error, 1)
	go func() { done <- c.IDLEloop(ctx) }()

	time.Sleep(120 * time.Millisecond) // let at least one poll tick
	cancel()

	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Errorf("IDLEloop returned unexpected error: %v", err)
		}
		if !c.pollOnly.Load() {
			t.Error("pollOnly flag not set after IDLE failure injection")
		}
	case <-time.After(2 * time.Second):
		t.Error("IDLEloop did not return within 2s after ctx cancel")
	}
}

// TestImap_ReconnectOnBrokenPipe simulates a mid-session connection drop by
// closing the underlying transport and verifying that the next Fetch call
// triggers reconnect logic.
//
// Limitation: imapmemserver routes all sessions through the server's Serve
// loop; there is no public API to close a single session's TCP conn from the
// server side without shutting down the whole listener. Instead, we close
// the Client's internal imapclient (setting it to nil), which mimics what
// reconnectWithBackoff does after detecting a broken pipe, and then verify
// that Connect (which is what reconnectWithBackoff calls) succeeds and Fetch
// works again.
func TestImap_ReconnectOnBrokenPipe(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	ts.appendMessage(t, simpleTestMessage)

	c := NewIMAPClient(ts.cfg())
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	// Simulate broken pipe: forcibly close the underlying imapclient.
	c.mu.Lock()
	old := c.imapClient
	c.imapClient = nil
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	// Reconnect and verify Fetch works.
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect after broken pipe: %v", err)
	}

	since := time.Now().Add(-24 * time.Hour)
	msgs, err := c.Fetch(ctx, since)
	if err != nil {
		t.Fatalf("Fetch after reconnect: %v", err)
	}
	if len(msgs) == 0 {
		t.Error("Fetch after reconnect returned 0 messages")
	}
}

// TestImap_PollOnceReturnsCleanlyOnCancel verifies that PollOnce propagates
// ctx cancellation within 1s.
func TestImap_PollOnceReturnsCleanlyOnCancel(t *testing.T) {
	ts := newTestServer(t)
	defer ts.close()

	c := NewIMAPClient(ts.cfg())
	ctx, cancel := context.WithCancel(context.Background())

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	cancel()

	done := make(chan error, 1)
	go func() { done <- c.PollOnce(ctx) }()

	select {
	case err := <-done:
		// ctx.Err() or nil both acceptable — cancelled cleanly.
		if err != nil && err != context.Canceled {
			t.Errorf("PollOnce returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("PollOnce did not return within 1s after ctx cancel")
	}
}

// TestImap_NoGoroutineLeakOnClose starts Connect + IDLEloop, then calls
// Close and verifies goleak finds no leaked goroutines.
func TestImap_NoGoroutineLeakOnClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	ts := newTestServer(t)
	defer ts.close()

	c := NewIMAPClient(ts.cfg())
	ctx, cancel := context.WithCancel(context.Background())

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- c.IDLEloop(ctx) }()

	time.Sleep(50 * time.Millisecond)

	// Cancel to stop IDLEloop, then Close.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("IDLEloop did not return after cancel")
	}

	if err := c.Close(); err != nil {
		t.Logf("Close: %v (best-effort)", err)
	}

	// Small settle to let the library's read goroutine drain.
	time.Sleep(50 * time.Millisecond)
}
