package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors for the SMTP client. Callers (queue worker D-B1.10)
// discriminate via errors.Is to decide retry-vs-failed.
var (
	// ErrSmtpPortNotSubmission — config selects a port other than 587
	// (STARTTLS submission) or 465 (implicit TLS submission). Direct
	// MX delivery is operator-responsibility per design-spec §17 AC #10;
	// the client never opens port 25.
	ErrSmtpPortNotSubmission = errors.New("smtp: port is not a submission port (require 587 with STARTTLS or 465 implicit TLS)")

	// ErrSmtpTransient — 4xx response. Queue worker retries with backoff.
	ErrSmtpTransient = errors.New("smtp: transient failure")

	// ErrSmtpPermanent — 5xx response. Queue worker drops to failed.
	ErrSmtpPermanent = errors.New("smtp: permanent failure")
)

// SMTPConfig holds the connection + auth parameters resolved from
// EmailConfig.SMTP + EmailSecrets.SMTPPassword. TLSRootCAs is a test-only
// override that lets the fake-server unit tests trust a self-signed
// certificate without disabling verification globally; production paths
// pass nil and the standard system pool is used.
//
// InsecureSkipVerify is the same field as EmailSMTP.InsecureSkipVerify
// (carrying json:"-") — it is set programmatically by tests only and
// must never be deserialized from .thrum/config.json.
type SMTPConfig struct {
	Host               string
	Port               int
	UseStartTLS        bool // true: STARTTLS on 587; false: implicit TLS on 465
	Username           string
	Password           string
	InsecureSkipVerify bool
	TLSRootCAs         *x509.CertPool // optional; nil → system pool
}

// Envelope is the input to SMTPClient.Submit: a prepared MIME message
// (raw bytes from ComposeAgentMessage / ComposeProtocolMessage) plus
// the SMTP-level envelope addresses. From / To are bare addresses
// without the angle brackets; the client adds those on the wire.
type Envelope struct {
	From string
	To   []string
	Raw  []byte
}

// SMTPClient submits prepared envelopes via authenticated SMTP. It is
// stateless across submissions — each call opens a fresh connection.
// Persistent connections + pipelining are deferred (v0.12+); v0.11
// volume estimates show per-submission dial overhead is negligible.
type SMTPClient struct {
	cfg SMTPConfig
}

// NewSMTPClient validates the port + returns a ready-to-Submit client.
// Returns ErrSmtpPortNotSubmission when the configured port is not 587
// (STARTTLS) or 465 (implicit TLS). The validation runs at construction
// so misconfiguration surfaces at daemon-startup, not first-send.
func NewSMTPClient(cfg SMTPConfig) (*SMTPClient, error) {
	if cfg.Port == 587 && cfg.UseStartTLS {
		return &SMTPClient{cfg: cfg}, nil
	}
	if cfg.Port == 465 && !cfg.UseStartTLS {
		return &SMTPClient{cfg: cfg}, nil
	}
	return nil, fmt.Errorf("%w: got port=%d use_starttls=%v", ErrSmtpPortNotSubmission, cfg.Port, cfg.UseStartTLS)
}

// Submit opens a connection to the configured server, performs the full
// SMTP handshake + AUTH PLAIN, and uploads the envelope. Returns a typed
// error so the queue worker can decide retry-vs-failed.
func (c *SMTPClient) Submit(ctx context.Context, env Envelope) error {
	addr := net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
	return c.SubmitWithAddr(ctx, addr, env)
}

// SubmitWithAddr is the test-facing entry point — bypasses port
// validation so the fake server can listen on an ephemeral port. The
// production path goes through Submit which derives the addr from
// validated config.
func (c *SMTPClient) SubmitWithAddr(ctx context.Context, addr string, env Envelope) error {
	conn, err := c.dial(ctx, addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Once the TCP connection is open we still need to honor ctx-cancel
	// for the protocol exchange. net/smtp doesn't take a context, so
	// we wire it via SetDeadline whenever ctx has one — the connection's
	// read/write deadlines fire as context becomes due.
	c.applyDeadline(conn, ctx)

	// stopWatch propagates ctx.Done to the connection so an in-flight
	// DATA upload returns promptly when the caller cancels. Without it,
	// the goroutine would block on the slow server until net/smtp's own
	// timeout fires (potentially minutes).
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			// Forcing the deadline into the past unblocks any
			// pending Read/Write with a deadline-exceeded error.
			_ = conn.SetDeadline(time.Unix(1, 0))
		case <-stopWatch:
		}
	}()

	client, err := smtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		return c.wrapErr("new client", err, ctx)
	}
	defer func() { _ = client.Quit() }()

	if err := client.Hello("localhost"); err != nil {
		return c.wrapErr("EHLO", err, ctx)
	}

	if c.cfg.UseStartTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return fmt.Errorf("smtp: server does not advertise STARTTLS")
		}
		if err := client.StartTLS(c.tlsConfig()); err != nil {
			return c.wrapErr("STARTTLS", err, ctx)
		}
	}

	auth := smtp.PlainAuth("", c.cfg.Username, c.cfg.Password, c.cfg.Host)
	if err := client.Auth(auth); err != nil {
		return c.wrapErr("AUTH", err, ctx)
	}

	if err := client.Mail(env.From); err != nil {
		return c.wrapErr("MAIL FROM", err, ctx)
	}
	for _, to := range env.To {
		if err := client.Rcpt(to); err != nil {
			return c.wrapErr("RCPT TO", err, ctx)
		}
	}

	w, err := client.Data()
	if err != nil {
		return c.wrapErr("DATA", err, ctx)
	}
	if _, err := w.Write(env.Raw); err != nil {
		return c.wrapErr("DATA body", err, ctx)
	}
	if err := w.Close(); err != nil {
		// DATA-end close returns the server's final response — this is
		// where 4xx / 5xx classification matters most.
		return c.wrapErr("DATA close", err, ctx)
	}

	return nil
}

// dial opens the TCP (or TLS for implicit-TLS port 465) connection.
// Honors ctx-cancellation during the dial via net.Dialer.DialContext.
func (c *SMTPClient) dial(ctx context.Context, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	if c.cfg.UseStartTLS {
		return dialer.DialContext(ctx, "tcp", addr)
	}
	// Implicit TLS (port 465): wrap the dial in a TLS handshake.
	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	tlsConn := tls.Client(rawConn, c.tlsConfig())
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func (c *SMTPClient) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName:         c.cfg.Host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: c.cfg.InsecureSkipVerify, //nolint:gosec // gated by EmailSMTP.InsecureSkipVerify which carries json:"-"; never deserialized from config.json
		RootCAs:            c.cfg.TLSRootCAs,
	}
}

// applyDeadline pushes the ctx deadline (if any) onto the underlying
// connection so net/smtp's protocol reads/writes honor it.
func (c *SMTPClient) applyDeadline(conn net.Conn, ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
}

// wrapErr classifies an SMTP error into the typed sentinels. The
// net/smtp client returns errors of the form "NNN message" — the
// classifier extracts the leading 3-digit code and routes 4xx to
// transient + 5xx to permanent. ctx-cancellation takes precedence
// (wraps as ctx.Err()) so callers can use errors.Is(err, context.Canceled).
func (c *SMTPClient) wrapErr(stage string, err error, ctx context.Context) error {
	if ctx.Err() != nil {
		return fmt.Errorf("smtp %s: %w", stage, ctx.Err())
	}
	// A net-level deadline-exceeded that wasn't picked up via ctx.Err()
	// above can happen when the conn deadline fires fractionally before
	// the parent context (clock-drift between Go's monotonic context
	// timer and kernel net deadlines). Route it the same way callers
	// expect — as DeadlineExceeded — so the queue worker treats it as
	// retryable cancellation rather than a transient SMTP failure.
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("smtp %s: %w", stage, context.DeadlineExceeded)
	}

	code := extractSMTPCode(err.Error())
	switch {
	case code >= 400 && code < 500:
		return fmt.Errorf("%w (%s): %v", ErrSmtpTransient, stage, err)
	case code >= 500 && code < 600:
		return fmt.Errorf("%w (%s): %v", ErrSmtpPermanent, stage, err)
	default:
		// No 3-digit code in the message — likely a network-layer error
		// (broken pipe, EOF, TLS handshake). Treat as transient: most
		// network errors are recoverable on retry; truly permanent
		// configuration problems surface either at startup (port
		// validation) or via a 5xx server response.
		return fmt.Errorf("%w (%s): %v", ErrSmtpTransient, stage, err)
	}
}

// extractSMTPCode pulls the leading 3-digit reply code from a net/smtp
// error message. Returns 0 if no code is present.
func extractSMTPCode(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 3 {
		return 0
	}
	c1, c2, c3 := s[0], s[1], s[2]
	if c1 < '0' || c1 > '9' || c2 < '0' || c2 > '9' || c3 < '0' || c3 > '9' {
		return 0
	}
	code, _ := strconv.Atoi(s[:3])
	return code
}

// Ensure unused-import elision. bufio is used in test helpers only; we
// don't reference it from production code but keep the import group tidy.
var _ = bufio.NewReader
