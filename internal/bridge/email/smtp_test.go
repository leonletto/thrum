package email_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/bridge/email"
)

// D-B1.5 — Authenticated SMTP submission with typed retry errors.
// Direct MX delivery is NOT supported (operator-responsibility per
// spec §17 AC #10); the client rejects any port other than 587 or 465.

// --- minimal fake SMTP server for the happy-path + error-classification tests ---

type fakeSMTPBehavior struct {
	// reply override map: SMTP verb prefix → response line
	// Empty/missing key uses the default 2xx success.
	replies map[string]string
	// slowData: when true, the server sleeps mid-DATA so the client's
	// ctx-cancellation path can be exercised.
	slowData bool
	// useStartTLS: advertise STARTTLS in the EHLO response and accept
	// the upgrade. When false, plain SMTP only (used by port-25-reject
	// test where the dial never succeeds anyway).
	useStartTLS bool
	tlsConfig   *tls.Config
}

type fakeSMTPServer struct {
	listener net.Listener
	addr     string
	port     int
	behavior *fakeSMTPBehavior
	done     chan struct{}
}

func newFakeSMTPServer(t *testing.T, behavior *fakeSMTPBehavior) *fakeSMTPServer {
	t.Helper()
	if behavior.replies == nil {
		behavior.replies = map[string]string{}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	srv := &fakeSMTPServer{
		listener: ln,
		addr:     addr.IP.String(),
		port:     addr.Port,
		behavior: behavior,
		done:     make(chan struct{}),
	}

	go srv.serve()

	t.Cleanup(func() {
		_ = ln.Close()
		<-srv.done
	})
	return srv
}

func (s *fakeSMTPServer) serve() {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTPServer) handle(rawConn net.Conn) {
	defer func() { _ = rawConn.Close() }()
	conn := rawConn

	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	writeLine := func(line string) {
		_, _ = w.WriteString(line + "\r\n")
		_ = w.Flush()
	}

	writeLine("220 fake.local ESMTP ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"):
			writeLine("250-fake.local")
			writeLine("250-AUTH PLAIN LOGIN")
			if s.behavior.useStartTLS {
				writeLine("250-STARTTLS")
			}
			writeLine("250 OK")
		case strings.HasPrefix(upper, "HELO"):
			writeLine("250 fake.local")
		case strings.HasPrefix(upper, "STARTTLS"):
			writeLine("220 ready for TLS")
			tlsConn := tls.Server(conn, s.behavior.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			w = bufio.NewWriter(conn)
			r = bufio.NewReader(conn)
		case strings.HasPrefix(upper, "AUTH"):
			// Consume AUTH PLAIN credential exchange.
			if reply, ok := s.behavior.replies["AUTH"]; ok {
				writeLine(reply)
			} else {
				writeLine("235 2.7.0 Authentication successful")
			}
		case strings.HasPrefix(upper, "MAIL FROM"):
			if reply, ok := s.behavior.replies["MAIL"]; ok {
				writeLine(reply)
			} else {
				writeLine("250 OK")
			}
		case strings.HasPrefix(upper, "RCPT TO"):
			if reply, ok := s.behavior.replies["RCPT"]; ok {
				writeLine(reply)
			} else {
				writeLine("250 OK")
			}
		case strings.HasPrefix(upper, "DATA"):
			writeLine("354 End data with <CR><LF>.<CR><LF>")
			// Read until lone "." then respond. slowData inserts an
			// interruptible wait so the ctx-cancel test exercises a
			// realistic mid-DATA stall without leaking a 2s goroutine
			// past test cleanup (goleak in TestImap_NoGoroutineLeakOnClose
			// would otherwise catch the orphan).
			for {
				dl, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if s.behavior.slowData {
					select {
					case <-time.After(2 * time.Second):
					case <-s.done:
						return
					}
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
			}
			if reply, ok := s.behavior.replies["DATA_END"]; ok {
				writeLine(reply)
			} else {
				writeLine("250 OK queued as ABCD")
			}
		case strings.HasPrefix(upper, "QUIT"):
			writeLine("221 bye")
			return
		case strings.HasPrefix(upper, "RSET"), strings.HasPrefix(upper, "NOOP"):
			writeLine("250 OK")
		default:
			writeLine("502 Command not recognized")
		}
	}
}

// genSelfSignedCert produces a fresh self-signed certificate covering
// 127.0.0.1 + localhost so the fake server can complete a TLS handshake
// without checking in cert+key PEM constants.
func genSelfSignedCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
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
		t.Fatalf("create cert: %v", err)
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

// makeEnvelope returns a minimal envelope suitable for the fake server's
// MAIL/RCPT/DATA flow.
func makeEnvelope() email.Envelope {
	return email.Envelope{
		From: "thrum-mesh@fake.local",
		To:   []string{"recipient@fake.local"},
		Raw:  []byte("From: <thrum-mesh@fake.local>\r\nTo: <recipient@fake.local>\r\nSubject: hi\r\n\r\nbody\r\n"),
	}
}

func TestSmtp_SubmitOver587Starttls(t *testing.T) {
	cert, pool := genSelfSignedCert(t)
	srv := newFakeSMTPServer(t, &fakeSMTPBehavior{
		useStartTLS: true,
		tlsConfig:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	})

	// Construct with prod-valid port=587 to exercise port validation; the
	// actual dial goes to srv.port via SubmitWithAddr so the fake server
	// can listen on an ephemeral port. Port validation is separately
	// exercised by TestSmtp_RejectsPort25.
	c, err := email.NewSMTPClient(email.SMTPConfig{
		Host:        "127.0.0.1",
		Port:        587,
		UseStartTLS: true,
		Username:    "user",
		Password:    "pw",
		TLSRootCAs:  pool,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if err := c.SubmitWithAddr(t.Context(), net.JoinHostPort("127.0.0.1", itoa(srv.port)), makeEnvelope()); err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func TestSmtp_RejectsPort25(t *testing.T) {
	_, err := email.NewSMTPClient(email.SMTPConfig{
		Host:        "smtp.example.com",
		Port:        25,
		UseStartTLS: true,
	})
	if err == nil {
		t.Fatal("expected port-25 rejection, got nil")
	}
	if !errors.Is(err, email.ErrSmtpPortNotSubmission) {
		t.Errorf("expected ErrSmtpPortNotSubmission, got %v", err)
	}

	// 465 implicit-TLS allowed
	if _, err := email.NewSMTPClient(email.SMTPConfig{
		Host:        "smtp.example.com",
		Port:        465,
		UseStartTLS: false,
	}); err != nil {
		t.Errorf("expected port-465 accepted, got %v", err)
	}

	// 587 STARTTLS allowed
	if _, err := email.NewSMTPClient(email.SMTPConfig{
		Host:        "smtp.example.com",
		Port:        587,
		UseStartTLS: true,
	}); err != nil {
		t.Errorf("expected port-587 accepted, got %v", err)
	}
}

func TestSmtp_ClassifiesTransient450(t *testing.T) {
	cert, pool := genSelfSignedCert(t)
	srv := newFakeSMTPServer(t, &fakeSMTPBehavior{
		useStartTLS: true,
		tlsConfig:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		replies:     map[string]string{"DATA_END": "450 4.7.1 Try again later"},
	})

	c, err := email.NewSMTPClient(email.SMTPConfig{
		Host:        "127.0.0.1",
		Port:        587,
		UseStartTLS: true,
		Username:    "user",
		Password:    "pw",
		TLSRootCAs:  pool,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	err = c.SubmitWithAddr(t.Context(), net.JoinHostPort("127.0.0.1", itoa(srv.port)), makeEnvelope())
	if err == nil {
		t.Fatal("expected transient error, got nil")
	}
	if !errors.Is(err, email.ErrSmtpTransient) {
		t.Errorf("expected ErrSmtpTransient, got %v", err)
	}
}

func TestSmtp_ClassifiesPermanent550(t *testing.T) {
	cert, pool := genSelfSignedCert(t)
	srv := newFakeSMTPServer(t, &fakeSMTPBehavior{
		useStartTLS: true,
		tlsConfig:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		replies:     map[string]string{"DATA_END": "550 5.7.1 Permission denied"},
	})

	c, err := email.NewSMTPClient(email.SMTPConfig{
		Host:        "127.0.0.1",
		Port:        587,
		UseStartTLS: true,
		Username:    "user",
		Password:    "pw",
		TLSRootCAs:  pool,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	err = c.SubmitWithAddr(t.Context(), net.JoinHostPort("127.0.0.1", itoa(srv.port)), makeEnvelope())
	if err == nil {
		t.Fatal("expected permanent error, got nil")
	}
	if !errors.Is(err, email.ErrSmtpPermanent) {
		t.Errorf("expected ErrSmtpPermanent, got %v", err)
	}
	if !strings.Contains(err.Error(), "550") {
		t.Errorf("expected permanent error to carry response code; got %v", err)
	}
}

func TestSmtp_RespectsContextCancellation(t *testing.T) {
	cert, pool := genSelfSignedCert(t)
	srv := newFakeSMTPServer(t, &fakeSMTPBehavior{
		useStartTLS: true,
		tlsConfig:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		slowData:    true,
	})

	c, err := email.NewSMTPClient(email.SMTPConfig{
		Host:        "127.0.0.1",
		Port:        587,
		UseStartTLS: true,
		Username:    "user",
		Password:    "pw",
		TLSRootCAs:  pool,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = c.SubmitWithAddr(ctx, net.JoinHostPort("127.0.0.1", itoa(srv.port)), makeEnvelope())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected ctx-cancellation error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected ctx error, got %v", err)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("submit did not return promptly on ctx cancel (took %v)", elapsed)
	}
}

func TestSmtp_TLSVerificationFailsOnSelfSigned(t *testing.T) {
	cert, _ := genSelfSignedCert(t) // pool deliberately omitted from client config
	srv := newFakeSMTPServer(t, &fakeSMTPBehavior{
		useStartTLS: true,
		tlsConfig:   &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
	})

	c, err := email.NewSMTPClient(email.SMTPConfig{
		Host:        "127.0.0.1",
		Port:        587,
		UseStartTLS: true,
		Username:    "user",
		Password:    "pw",
		// No TLSRootCAs override → uses system pool, which doesn't trust
		// our self-signed cert. InsecureSkipVerify also left false.
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	err = c.SubmitWithAddr(t.Context(), net.JoinHostPort("127.0.0.1", itoa(srv.port)), makeEnvelope())
	if err == nil {
		t.Fatal("expected TLS verification failure, got nil")
	}
	if !strings.Contains(err.Error(), "x509") && !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "tls") {
		t.Errorf("expected TLS / certificate verification error, got %v", err)
	}
}

// itoa is a tiny helper to avoid pulling strconv into the test file's
// import list for one call-site per test.
func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

// Sanity: discard unused imports if compiler flags them.
var _ = io.EOF
