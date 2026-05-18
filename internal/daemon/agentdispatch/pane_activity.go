package agentdispatch

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// PaneActivity returns the wall-clock time of the most recent
// activity on the named tmux pane via tmux's #{window_activity}
// format string. The value is the same primitive
// internal/daemon/rpc/tmux.go's nudgeSilentPaneAfter watchdog
// consumes (via tmuxLastActivityFn), surfaced here as a package-
// level helper so the E6.4 multi-fire idle-nudge loop can poll
// without reaching across packages.
//
// Per project rule feedback_byte_equality_pane_detection.md: this
// is the canonical race-free liveness signal — byte-equality on
// captured pane snapshots is forbidden because Claude Code's
// animated spinner produces false negatives. window_activity is
// monotonic per tmux session and updates on every keypress + every
// output line, so a stale value reliably means "the runtime is
// neither emitting nor receiving."
//
// Returns the activity timestamp on success, or an error wrapping
// the tmux invocation failure / parse failure. Callers ARE
// expected to handle transient errors (e.g., session torn down
// mid-poll) by deferring + retrying rather than failing the
// dispatch outright.
func PaneActivity(ctx context.Context, target string) (time.Time, error) {
	out, err := safecmd.Tmux(ctx, "display-message", "-t", target+":0.0", "-p", "#{window_activity}")
	if err != nil {
		return time.Time{}, fmt.Errorf("pane activity for %q: %w", target, err)
	}
	return parsePaneActivity(out)
}

// parsePaneActivity converts tmux's #{window_activity} output (a
// Unix-epoch integer with a trailing newline) to a time.Time.
// Factored out for unit testing without requiring a real tmux
// subprocess.
//
// Empty output is the "tmux returned nothing" case and gets a
// dedicated error so callers can distinguish "session missing"
// from "session present but produced garbage". The latter
// signals an upstream tmux change worth investigating; the former
// is benign mid-teardown noise.
func parsePaneActivity(out []byte) (time.Time, error) {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, fmt.Errorf("empty window_activity output")
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse window_activity %q: %w", s, err)
	}
	if secs < 0 {
		return time.Time{}, fmt.Errorf("negative window_activity timestamp: %d", secs)
	}
	return time.Unix(secs, 0), nil
}
