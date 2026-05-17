// Package harness provides shared fake-IMAP and fake-SMTP infrastructure for
// the email integration tests under tests/integration/email/.
//
// Design goals:
//   - Fake IMAP server: lifted from internal/bridge/email/imap_test.go
//     (go-imap v2 in-memory server over plain TCP + STARTTLS); shared cert+key
//     PEM constants live here once to avoid duplication across test files.
//   - Fake SMTP server: lifted from internal/bridge/email/smtp_test.go
//     (hand-rolled ESMTP dialog handler); recording mode so tests assert on
//     received envelopes.
//   - MailServer: a combined façade exposing inject-from-A / fetch-as-B
//     semantics so cross-daemon tests don't handle raw RFC 5322 envelopes.
//
// Both servers listen on localhost:0 (OS-assigned ephemeral port) and are
// registered for t.Cleanup so tests don't need explicit teardown.

package harness

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// IMAPCredentials holds the login username and password used by the fake server.
const (
	TestIMAPUser = "test-user"
	TestIMAPPass = "test-password"
)

// --- Fake IMAP server (lifted from internal/bridge/email/imap_test.go) -------

// FakeIMAP wraps an in-memory go-imap v2 server exposed over plain TCP with
// STARTTLS. The server is ready to accept connections as soon as New returns.
type FakeIMAP struct {
	server    *imapserver.Server
	listener  net.Listener
	User      *imapmemserver.User
	MemSrv    *imapmemserver.Server
	ClientTLS *tls.Config // InsecureSkipVerify: for client-side connections

	cert tls.Certificate
}

// NewFakeIMAP creates and starts an in-memory IMAP server on an OS-chosen
// ephemeral port. Registers t.Cleanup to close the listener.
func NewFakeIMAP(t *testing.T) *FakeIMAP {
	t.Helper()

	cert, pool := genSelfSignedCert(t)

	memSrv := imapmemserver.New()
	user := imapmemserver.NewUser(TestIMAPUser, TestIMAPPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("harness: create INBOX: %v", err)
	}
	memSrv.AddUser(user)

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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("harness: net.Listen: %v", err)
	}

	go func() {
		_ = srv.Serve(ln)
	}()

	t.Cleanup(func() { _ = ln.Close() })

	clientTLS := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // test only
		RootCAs:            pool,
	}

	return &FakeIMAP{
		server:    srv,
		listener:  ln,
		User:      user,
		MemSrv:    memSrv,
		ClientTLS: clientTLS,
		cert:      cert,
	}
}

// Addr returns "host:port" for the listening socket.
func (f *FakeIMAP) Addr() string {
	return f.listener.Addr().String()
}

// Host returns the hostname part of the listener address.
func (f *FakeIMAP) Host() string {
	h, _, _ := net.SplitHostPort(f.listener.Addr().String())
	return h
}

// Port returns the port number of the listener.
func (f *FakeIMAP) Port() int {
	addr, ok := f.listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	return addr.Port
}

// AppendMessage delivers a raw RFC 5322 message to INBOX via the IMAP APPEND
// command. Uses a plain-TCP connection (InsecureAuth=true on server).
func (f *FakeIMAP) AppendMessage(t *testing.T, raw string) {
	t.Helper()
	conn, err := net.Dial("tcp", f.Addr())
	if err != nil {
		t.Fatalf("harness: dial imap: %v", err)
	}
	cl := imapclient.New(conn, nil)
	defer func() { _ = cl.Close() }()

	if err := cl.Login(TestIMAPUser, TestIMAPPass).Wait(); err != nil {
		t.Fatalf("harness: imap login: %v", err)
	}

	data := []byte(raw)
	appendCmd := cl.Append("INBOX", int64(len(data)), nil)
	if _, err := appendCmd.Write(data); err != nil {
		t.Fatalf("harness: imap append write: %v", err)
	}
	if err := appendCmd.Close(); err != nil {
		t.Fatalf("harness: imap append close: %v", err)
	}
	if _, err := appendCmd.Wait(); err != nil {
		t.Fatalf("harness: imap append wait: %v", err)
	}
}

// --- Fake SMTP server (lifted from internal/bridge/email/smtp_test.go) --------

// ReceivedEnvelope records one SMTP transaction: MAIL FROM, RCPT TO, and DATA.
type ReceivedEnvelope struct {
	From string
	To   []string
	Data string
}

// FakeSMTP is a hand-rolled ESMTP server that records all submitted envelopes.
type FakeSMTP struct {
	listener   net.Listener
	TLSCfg     *tls.Config    // server-side
	ClientPool *x509.CertPool // for client-side TLS verification

	mu       sync.Mutex
	received []ReceivedEnvelope
	done     chan struct{}

	// replyOverride: SMTP verb prefix → custom response (used by error-injection tests)
	replyOverride map[string]string
}

// NewFakeSMTP creates and starts a fake SMTP server on an OS-chosen ephemeral
// port. Registers t.Cleanup to close the listener and drain the serve goroutine.
func NewFakeSMTP(t *testing.T) *FakeSMTP {
	t.Helper()

	cert, pool := genSelfSignedCert(t)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("harness: fake SMTP listen: %v", err)
	}

	s := &FakeSMTP{
		listener:      ln,
		TLSCfg:        tlsCfg,
		ClientPool:    pool,
		done:          make(chan struct{}),
		replyOverride: make(map[string]string),
	}

	go s.serve()

	t.Cleanup(func() {
		_ = ln.Close()
		<-s.done
	})
	return s
}

// Addr returns "host:port" for the listening socket.
func (s *FakeSMTP) Addr() string { return s.listener.Addr().String() }

// Port returns the port number.
func (s *FakeSMTP) Port() int {
	addr, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	return addr.Port
}

// SetReplyOverride lets a test inject a custom SMTP response for a given verb prefix.
func (s *FakeSMTP) SetReplyOverride(verbPrefix, response string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replyOverride[strings.ToUpper(verbPrefix)] = response
}

// Received returns a snapshot of all envelopes submitted so far.
func (s *FakeSMTP) Received() []ReceivedEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]ReceivedEnvelope, len(s.received))
	copy(cp, s.received)
	return cp
}

// InjectEnvelope directly appends an envelope to the received list without
// going through the SMTP protocol path. Used by in-process adapters that
// want to record what would have been submitted to a real SMTP server.
func (s *FakeSMTP) InjectEnvelope(env ReceivedEnvelope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, env)
}

// WaitForN blocks until at least n envelopes have been received or timeout elapses.
func (s *FakeSMTP) WaitForN(t *testing.T, n int, timeout time.Duration) []ReceivedEnvelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := s.Received(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("harness: timed out waiting for %d SMTP envelopes (got %d)", n, len(s.Received()))
	return nil
}

func (s *FakeSMTP) serve() {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *FakeSMTP) handle(rawConn net.Conn) {
	defer func() { _ = rawConn.Close() }()

	var conn = rawConn
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)

	writeLine := func(line string) {
		_, _ = w.WriteString(line + "\r\n")
		_ = w.Flush()
	}

	writeLine("220 fake.local ESMTP ready")

	var mailFrom string
	var rcptTo []string

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		s.mu.Lock()
		overrides := make(map[string]string, len(s.replyOverride))
		for k, v := range s.replyOverride {
			overrides[k] = v
		}
		s.mu.Unlock()

		// replyOrDefault returns the override for any stored key that is a
		// prefix of upper, falling back to dflt. The check is against upper
		// so keys are matched case-insensitively (they were stored upper-cased).
		replyOrDefault := func(dflt string) string {
			for k, v := range overrides {
				if strings.HasPrefix(upper, k) {
					return v
				}
			}
			return dflt
		}

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			writeLine("250-fake.local")
			writeLine("250-AUTH PLAIN LOGIN")
			writeLine("250-STARTTLS")
			writeLine("250 OK")
		case strings.HasPrefix(upper, "STARTTLS"):
			writeLine("220 ready for TLS")
			tlsConn := tls.Server(conn, s.TLSCfg)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			w = bufio.NewWriter(conn)
			r = bufio.NewReader(conn)
		case strings.HasPrefix(upper, "AUTH"):
			writeLine(replyOrDefault("235 2.7.0 Authentication successful"))
		case strings.HasPrefix(upper, "MAIL FROM"):
			// Extract address from MAIL FROM:<addr>
			if idx := strings.Index(line, "<"); idx >= 0 {
				end := strings.Index(line[idx:], ">")
				if end >= 0 {
					mailFrom = line[idx+1 : idx+end]
				}
			}
			writeLine(replyOrDefault("250 OK"))
		case strings.HasPrefix(upper, "RCPT TO"):
			if idx := strings.Index(line, "<"); idx >= 0 {
				end := strings.Index(line[idx:], ">")
				if end >= 0 {
					rcptTo = append(rcptTo, line[idx+1:idx+end])
				}
			}
			writeLine(replyOrDefault("250 OK"))
		case strings.HasPrefix(upper, "DATA"):
			writeLine("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				dl, err := r.ReadString('\n')
				if err != nil {
					return
				}
				trimmed := strings.TrimRight(dl, "\r\n")
				if trimmed == "." {
					break
				}
				body.WriteString(dl)
			}
			env := ReceivedEnvelope{
				From: mailFrom,
				To:   append([]string(nil), rcptTo...),
				Data: body.String(),
			}
			s.mu.Lock()
			s.received = append(s.received, env)
			s.mu.Unlock()

			// Reset for next transaction
			mailFrom = ""
			rcptTo = nil

			writeLine(replyOrDefault(fmt.Sprintf("250 OK queued as %d", len(s.received))))
		case strings.HasPrefix(upper, "QUIT"):
			writeLine("221 bye")
			return
		case strings.HasPrefix(upper, "RSET"):
			mailFrom = ""
			rcptTo = nil
			writeLine("250 OK")
		default:
			writeLine("502 Command not recognized")
		}
	}
}

// --- shared TLS cert generation helper (avoids duplicating PEM constants) -----

// genSelfSignedCert generates a fresh self-signed RSA cert covering 127.0.0.1
// and localhost. Returns (cert for server, pool for client root CA pinning).
func genSelfSignedCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("harness: rsa.GenerateKey: %v", err)
	}

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake.local"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", "fake.local"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("harness: CreateCertificate: %v", err)
	}

	cert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
	}
	pool := x509.NewCertPool()
	parsed, _ := x509.ParseCertificate(der)
	pool.AddCert(parsed)
	return cert, pool
}
