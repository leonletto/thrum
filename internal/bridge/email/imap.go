package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// idleFailureThreshold is the number of consecutive IDLE errors that trigger
// a downgrade to poll-only mode for the remainder of the session.
const idleFailureThreshold = 3

// Reconnect backoff schedule: 5s, 15s, 45s, then capped at 5 min.
var reconnectBackoff = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	45 * time.Second,
	5 * time.Minute,
}

const maxReconnectAttempts = 10

// IMAPConfig holds all parameters for an IMAPClient.
type IMAPConfig struct {
	Host         string
	Port         int
	UseStartTLS  bool
	UseIDLE      bool
	Username     string
	Password     string
	PollInterval time.Duration
	// TLSConfig is optional; nil uses the library default (InsecureSkipVerify
	// is never set in production — tests supply a permissive config).
	TLSConfig *tls.Config
}

// RawMessage is the minimal envelope returned by Fetch.
type RawMessage struct {
	UID          imap.UID
	Bytes        []byte
	InternalDate time.Time
}

// IMAPClient wraps go-imap/v2 with IDLE keepalive, poll fallback, and
// reconnect-with-backoff.
type IMAPClient struct {
	cfg    IMAPConfig
	logger *log.Logger

	mu         sync.Mutex
	imapClient *imapclient.Client // guarded by mu

	// idleFailures counts consecutive IDLE start errors. When it reaches
	// idleFailureThreshold the session switches permanently to poll-only.
	idleFailures atomic.Int32
	pollOnly     atomic.Bool

	// reconnectAttempts counts successive reconnect tries since last success.
	reconnectAttempts int

	// closed is set by Close; prevents further reconnect loops.
	closed atomic.Bool

	closeOnce sync.Once
	closeCh   chan struct{}
}

// NewIMAPClient returns an unconnected IMAPClient. Call Connect before
// any other method.
func NewIMAPClient(cfg IMAPConfig) *IMAPClient {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 60 * time.Second
	}
	return &IMAPClient{
		cfg:     cfg,
		logger:  log.New(os.Stderr, "imap: ", log.LstdFlags),
		closeCh: make(chan struct{}),
	}
}

// Connect dials the server and authenticates. It must be called before any
// other operation.
func (c *IMAPClient) Connect(ctx context.Context) error {
	return c.connect(ctx)
}

func (c *IMAPClient) connect(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)

	tlsCfg := c.cfg.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: c.cfg.Host}
	}

	opts := &imapclient.Options{TLSConfig: tlsCfg}

	var (
		rawConn net.Conn
		cl      *imapclient.Client
		err     error
	)

	if c.cfg.UseStartTLS {
		rawConn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("imap dial: %w", err)
		}
		cl, err = imapclient.NewStartTLS(rawConn, opts)
	} else {
		// Implicit TLS — DialTLS does not accept a context; wrap with Dialer.
		dialer := &net.Dialer{}
		rawConn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("imap tls dial: %w", err)
		}
		cl = imapclient.New(rawConn, opts)
	}

	if err != nil {
		if rawConn != nil {
			_ = rawConn.Close()
		}
		return err
	}

	// AUTH PLAIN via the LOGIN command (imapclient.Login sends RFC 3501 LOGIN,
	// which the test server accepts because InsecureAuth is set).
	if err := cl.Login(c.cfg.Username, c.cfg.Password).Wait(); err != nil {
		_ = cl.Close()
		return fmt.Errorf("imap login: %w", err)
	}

	// Select INBOX so Fetch / IDLE operate on the right mailbox.
	if _, err := cl.Select("INBOX", nil).Wait(); err != nil {
		_ = cl.Close()
		return fmt.Errorf("imap select INBOX: %w", err)
	}

	c.mu.Lock()
	c.imapClient = cl
	c.reconnectAttempts = 0
	c.mu.Unlock()

	return nil
}

// IDLEloop enters the IDLE command and blocks until ctx is canceled or a
// fatal error occurs. If IDLE fails idleFailureThreshold times in a row it
// downgrades to a timed-poll loop and returns nil.
//
// Re-IDLE is handled internally by go-imap/v2's IdleCommand (every 28 min by
// default). We enter IDLE and wait on ctx.Done; tearing down IDLE on cancel.
func (c *IMAPClient) IDLEloop(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.pollOnly.Load() {
			return c.pollLoop(ctx)
		}
		if !c.cfg.UseIDLE {
			return c.pollLoop(ctx)
		}

		err := c.runIDLE(ctx)
		if err == nil || ctx.Err() != nil {
			return ctx.Err()
		}

		// Transient IDLE error — count failures.
		n := c.idleFailures.Add(1)
		c.logger.Printf("IDLE error (#%d): %v", n, err)
		if n >= idleFailureThreshold {
			c.logger.Printf("IDLE failed %d times; degrading to poll-only for this session", n)
			c.pollOnly.Store(true)
			return c.pollLoop(ctx)
		}

		// Try reconnect before next IDLE attempt.
		if rerr := c.reconnectWithBackoff(ctx); rerr != nil {
			return rerr
		}
	}
}

// runIDLE enters IDLE, blocks on ctx, then tears it down.
func (c *IMAPClient) runIDLE(ctx context.Context) error {
	c.mu.Lock()
	cl := c.imapClient
	c.mu.Unlock()

	if cl == nil {
		return errors.New("not connected")
	}

	idleCmd, err := cl.Idle()
	if err != nil {
		return fmt.Errorf("idle start: %w", err)
	}

	// Block until ctx is canceled. The library handles 28-min re-IDLE
	// internally; our job is to tear down when we're told to stop.
	<-ctx.Done()
	if cerr := idleCmd.Close(); cerr != nil {
		c.logger.Printf("IDLE close: %v", cerr)
	}
	return nil
}

// pollLoop repeatedly calls PollOnce at cfg.PollInterval until ctx is canceled.
func (c *IMAPClient) pollLoop(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.PollOnce(ctx); err != nil && ctx.Err() == nil {
				c.logger.Printf("poll error: %v", err)
			}
		}
	}
}

// PollOnce is the A-B1 RegisterInternal entry point: a single fetch iteration
// that retrieves all unseen messages since the last 24 hours and processes
// them. Callers at the bridge layer wrap this in the handler shape from
// design-spec §13.
//
// Returns ctx.Err() promptly on cancellation.
func (c *IMAPClient) PollOnce(ctx context.Context) error {
	since := time.Now().Add(-24 * time.Hour)
	msgs, err := c.Fetch(ctx, since)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Attempt reconnect on fetch failure; surface if unrecoverable.
		if rerr := c.reconnectWithBackoff(ctx); rerr != nil {
			return rerr
		}
		// Retry once after reconnect.
		msgs, err = c.Fetch(ctx, since)
		if err != nil {
			return fmt.Errorf("poll fetch: %w", err)
		}
	}

	for _, msg := range msgs {
		if err := c.MarkSeen(ctx, msg.UID); err != nil && ctx.Err() == nil {
			c.logger.Printf("mark seen uid=%d: %v", msg.UID, err)
		}
	}
	return ctx.Err()
}

// Fetch searches for messages since `since` and returns their raw bytes.
// It uses UID SEARCH + UID FETCH BODY.PEEK[] to avoid marking messages seen.
func (c *IMAPClient) Fetch(ctx context.Context, since time.Time) ([]RawMessage, error) {
	c.mu.Lock()
	cl := c.imapClient
	c.mu.Unlock()

	if cl == nil {
		return nil, errors.New("not connected")
	}

	// UID SEARCH SINCE <date>
	criteria := &imap.SearchCriteria{Since: since}
	searchData, err := cl.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("uid search: %w", err)
	}

	uidSet, ok := searchData.All.(imap.UIDSet)
	if !ok || len(uidSet) == 0 {
		return nil, nil
	}

	// UID FETCH <set> (UID INTERNALDATE BODY.PEEK[])
	fetchOpts := &imap.FetchOptions{
		UID:          true,
		InternalDate: true,
		BodySection:  []*imap.FetchItemBodySection{{Peek: true}},
	}
	// Passing a imap.UIDSet as NumSet causes the client to issue UID FETCH.
	fetchCmd := cl.Fetch(uidSet, fetchOpts)

	var out []RawMessage
	for {
		msgData := fetchCmd.Next()
		if msgData == nil {
			break
		}
		buf, err := msgData.Collect()
		if err != nil {
			return out, fmt.Errorf("fetch collect: %w", err)
		}
		raw := RawMessage{
			UID:          buf.UID,
			InternalDate: buf.InternalDate,
		}
		section := &imap.FetchItemBodySection{Peek: true}
		raw.Bytes = buf.FindBodySection(section)
		out = append(out, raw)
	}
	if err := fetchCmd.Close(); err != nil {
		return out, fmt.Errorf("fetch close: %w", err)
	}
	return out, nil
}

// MarkSeen adds the \Seen flag to the message identified by uid.
func (c *IMAPClient) MarkSeen(ctx context.Context, uid imap.UID) error {
	c.mu.Lock()
	cl := c.imapClient
	c.mu.Unlock()

	if cl == nil {
		return errors.New("not connected")
	}

	storeCmd := cl.Store(imap.UIDSet{{Start: uid, Stop: uid}}, &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}, nil)
	return storeCmd.Close()
}

// Close tears down the IMAP connection. Safe to call multiple times.
func (c *IMAPClient) Close() error {
	var retErr error
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.closeCh)

		c.mu.Lock()
		cl := c.imapClient
		c.imapClient = nil
		c.mu.Unlock()

		if cl != nil {
			if err := cl.Logout().Wait(); err != nil {
				// Best-effort logout; swallow transient errors on shutdown.
				_ = err
			}
			retErr = cl.Close()
		}
	})
	return retErr
}

// reconnectWithBackoff attempts to reconnect up to maxReconnectAttempts with
// the configured backoff schedule. Returns an error when all attempts are
// exhausted or ctx is canceled.
func (c *IMAPClient) reconnectWithBackoff(ctx context.Context) error {
	c.mu.Lock()
	attempt := c.reconnectAttempts
	c.mu.Unlock()

	for {
		if c.closed.Load() || ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt >= maxReconnectAttempts {
			return fmt.Errorf("imap: exhausted %d reconnect attempts", maxReconnectAttempts)
		}

		delay := backoffDelay(attempt)
		c.logger.Printf("reconnect attempt %d/%d in %s", attempt+1, maxReconnectAttempts, delay)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closeCh:
			return nil
		case <-time.After(delay):
		}

		// Close the old connection before re-dialing.
		c.mu.Lock()
		old := c.imapClient
		c.imapClient = nil
		c.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}

		if err := c.connect(ctx); err != nil {
			c.logger.Printf("reconnect failed: %v", err)
			attempt++
			c.mu.Lock()
			c.reconnectAttempts = attempt
			c.mu.Unlock()
			continue
		}

		// Success.
		c.mu.Lock()
		c.reconnectAttempts = 0
		c.mu.Unlock()
		return nil
	}
}

// backoffDelay returns the delay for attempt n (0-indexed), capped at 5 min.
func backoffDelay(attempt int) time.Duration {
	if attempt >= len(reconnectBackoff) {
		return reconnectBackoff[len(reconnectBackoff)-1]
	}
	return reconnectBackoff[attempt]
}

