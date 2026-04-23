// internal/daemon/inbox/envelope.go
package inbox

import "time"

// Envelope is the minimal spool-file payload written by the daemon and
// consumed by the agent-side check-inbox hook script. Bodies are not
// included — agents retrieve them via `thrum inbox --unread` (matching
// the tmux-nudge pattern).
type Envelope struct {
	MsgID      string    `json:"msg_id"`
	From       string    `json:"from"`
	ReceivedAt time.Time `json:"received_at"`
}
