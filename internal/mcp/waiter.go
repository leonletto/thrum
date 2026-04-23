package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/daemon/rpc"
)

const (
	defaultWaitTimeout = 300 // seconds
	maxWaitTimeout     = 600 // seconds
	pollInterval       = 500 * time.Millisecond
)

// reconnectTimeout caps how long we'll retry connecting to the daemon
// before giving up and returning a timeout result. Declared as a var so
// tests can shrink it; production callers must not mutate at runtime.
var reconnectTimeout = 60 * time.Second

// Waiter polls the daemon inbox for messages addressed to this agent.
// It powers the wait_for_message MCP tool. The implementation mirrors the
// CLI `thrum wait` polling loop (internal/cli/wait.go): 500ms ticker,
// reconnect-on-failure, first-unseen-message wins.
//
// There is intentionally no WebSocket subscription. The previous design
// registered a "subscribe" RPC which was removed when CLI subscribe
// commands were deleted; polling shares the same message.list path the
// CLI already relies on.
type Waiter struct {
	socketPath string
	agentID    string // composite agent ID used for caller_agent_id + for_agent
	agentRole  string // agent role used for for_agent_role

	mu     sync.Mutex
	active bool

	ctx    context.Context
	cancel context.CancelFunc
}

// NewWaiter creates a polling Waiter. It never opens a network connection
// at construction time; connections are established lazily by WaitForMessage
// and torn down when the call returns.
func NewWaiter(parent context.Context, socketPath, agentID, agentRole string) *Waiter {
	ctx, cancel := context.WithCancel(parent)
	return &Waiter{
		socketPath: socketPath,
		agentID:    agentID,
		agentRole:  agentRole,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// WaitForMessage blocks until a message addressed to this agent arrives
// (by name, by role, or via @everyone broadcast) or the timeout expires.
// Only one wait may be active per Waiter instance.
func (w *Waiter) WaitForMessage(ctx context.Context, timeout int) (*WaitForMessageOutput, error) {
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	if timeout > maxWaitTimeout {
		timeout = maxWaitTimeout
	}

	w.mu.Lock()
	if w.active {
		w.mu.Unlock()
		return nil, fmt.Errorf("another wait_for_message is already active; only one waiter per agent")
	}
	w.active = true
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.active = false
		w.mu.Unlock()
	}()

	startTime := time.Now()
	// Look back 1 second so a message sent at the same instant the wait
	// starts still satisfies the created_after > threshold check. Mirrors
	// the CLI `thrum wait` default (cmd/thrum/main.go:1354). Without the
	// lookback, MCP clients that send-then-wait in tight succession would
	// race the poller and silently miss wakes.
	after := startTime.Add(-1 * time.Second).UTC()
	seen := make(map[string]bool)

	timeoutTimer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timeoutTimer.Stop()

	// pollTicker drives both the successful-poll cadence AND reconnect
	// retry cadence — a daemon outage uses the same tick to attempt
	// re-dial. Intentionally coupled: if pollInterval is ever split from
	// the reconnect rate, introduce a dedicated retryTicker here.
	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()

	var client *cli.Client
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()

	timeoutResult := func() *WaitForMessageOutput {
		return &WaitForMessageOutput{
			Status:        "timeout",
			WaitedSeconds: int(time.Since(startTime).Seconds()),
		}
	}

	// Initial connect is best-effort; retries happen on each poll tick.
	if c, err := cli.NewClient(w.socketPath); err == nil {
		client = c
	}

	// reconnectSince tracks when we first noticed the daemon was
	// unreachable so we can cap reconnection attempts at reconnectTimeout.
	var reconnectSince time.Time

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-w.ctx.Done():
			return nil, w.ctx.Err()
		case <-timeoutTimer.C:
			return timeoutResult(), nil
		case <-pollTicker.C:
			// Ensure we have a live client; try to open one if not.
			if client == nil {
				c, err := cli.NewClient(w.socketPath)
				if err != nil {
					if reconnectSince.IsZero() {
						reconnectSince = time.Now()
					} else if time.Since(reconnectSince) > reconnectTimeout {
						// Daemon has been unreachable for the full
						// reconnect budget. Surface as a structured
						// timeout rather than a tool error so MCP
						// clients get a uniform "no message" shape.
						slog.Warn("mcp.waiter daemon unreachable, returning timeout",
							"budget", reconnectTimeout.String(),
							"elapsed", time.Since(startTime).String())
						return timeoutResult(), nil
					}
					continue
				}
				client = c
				reconnectSince = time.Time{}
			}

			msg, err := w.pollOnce(client, after, seen)
			if err != nil {
				// RPC failure — likely daemon restart. Drop the client
				// and try to re-open on the next tick.
				_ = client.Close()
				client = nil
				if reconnectSince.IsZero() {
					reconnectSince = time.Now()
				}
				continue
			}
			if msg != nil {
				return &WaitForMessageOutput{
					Status:        "message_received",
					Message:       msg,
					WaitedSeconds: int(time.Since(startTime).Seconds()),
				}, nil
			}
		}
	}
}

// pollOnce performs a single message.list RPC with the waiter's filter
// criteria. Returns the first unseen message (oldest wins), or nil if no
// new message was found. Caller is responsible for reconnecting if err is
// non-nil.
func (w *Waiter) pollOnce(client *cli.Client, after time.Time, seen map[string]bool) (*MessageInfo, error) {
	params := map[string]any{
		"page_size":     10,
		"sort_by":       "created_at",
		"sort_order":    "desc",
		"created_after": after.UTC().Format(time.RFC3339Nano),
		"exclude_self":  true,
	}
	if w.agentID != "" {
		params["caller_agent_id"] = w.agentID
		params["for_agent"] = w.agentID
	}
	if w.agentRole != "" {
		params["for_agent_role"] = w.agentRole
	}

	var inbox cli.InboxResult
	if err := client.Call("message.list", params, &inbox); err != nil {
		return nil, err
	}

	// Inbox comes back newest-first; walk oldest-first so a backlog burst
	// returns in chronological order across successive waits.
	for i := len(inbox.Messages) - 1; i >= 0; i-- {
		m := inbox.Messages[i]
		if seen[m.MessageID] {
			continue
		}
		seen[m.MessageID] = true

		full, err := fetchMessageWithClient(client, m.MessageID)
		if err != nil {
			// Fall back to the list-row content if message.get fails —
			// the waker message is still legitimate, the fetch was
			// best-effort. The list-row RPC already succeeded, so any
			// error here is transient; the outer loop's reconnect
			// tracker intentionally does NOT advance on this path
			// (we still return a wake to the caller).
			return &MessageInfo{ //nolint:nilerr // deliberate: degrade to preview, caller gets the wake
				MessageID: m.MessageID,
				From:      m.AgentID,
				Content:   m.Body.Content,
				Timestamp: m.CreatedAt,
			}, nil
		}
		return full, nil
	}
	return nil, nil
}

// fetchMessageWithClient retrieves the full message body via message.get
// on the same daemon connection the poll used. Reusing the client keeps
// error-classification consistent with the outer reconnect tracker.
func fetchMessageWithClient(client *cli.Client, messageID string) (*MessageInfo, error) {
	var getResp rpc.GetMessageResponse
	if err := client.Call("message.get", rpc.GetMessageRequest{MessageID: messageID}, &getResp); err != nil {
		return nil, err
	}

	return &MessageInfo{
		MessageID: getResp.Message.MessageID,
		From:      getResp.Message.Author.AgentID,
		Content:   getResp.Message.Body.Content,
		Timestamp: getResp.Message.CreatedAt,
	}, nil
}

// Close signals the Waiter to stop any in-flight WaitForMessage call.
// Safe to call multiple times.
func (w *Waiter) Close() error {
	w.cancel()
	return nil
}
