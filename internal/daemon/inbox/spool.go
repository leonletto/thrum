// internal/daemon/inbox/spool.go
package inbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteSpool writes a nudge envelope for a single local agent to the
// spool dir at <thrumDir>/spool/<agentID>/<msg_id>.json. The write is
// atomic via temp-file + rename so hook readers never observe a
// half-written file.
//
// The caller is responsible for filtering out non-local recipients —
// WriteSpool assumes the agent exists on this daemon's host and that
// the caller has already made that determination.
//
// Non-nil errors are returned to the caller; callers should log and
// continue rather than failing the broader message pipeline.
func WriteSpool(thrumDir, agentID string, env Envelope) error {
	if thrumDir == "" || agentID == "" || env.MsgID == "" {
		return fmt.Errorf("inbox: invalid arguments (thrumDir, agentID, and env.MsgID are required)")
	}
	spoolDir := filepath.Join(thrumDir, "spool", agentID)
	if err := os.MkdirAll(spoolDir, 0o750); err != nil {
		return fmt.Errorf("inbox: mkdir spool dir: %w", err)
	}
	final := filepath.Join(spoolDir, env.MsgID+".json")
	tmp, err := os.CreateTemp(spoolDir, ".tmp-*.json")
	if err != nil {
		return fmt.Errorf("inbox: create temp: %w", err)
	}
	defer func() {
		// Cleanup on failure path; harmless on success (rename moves the inode).
		_ = os.Remove(tmp.Name())
	}()
	data, err := json.Marshal(env)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("inbox: marshal envelope: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("inbox: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("inbox: close temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return fmt.Errorf("inbox: rename temp to final: %w", err)
	}
	return nil
}
