package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leonletto/thrum/internal/schema"
)

// daemon_startwait.go — migration-aware daemon start-wait (thrum-vh2c).
//
// The daemon runs schema migrations during boot, BEFORE it listens on its Unix
// socket / writes ws.port. A long cross-version migration on a large DB (the
// reported case: v24->v41 on an 825MB DB) can outlast a fixed start-wait
// deadline, so `thrum daemon start/restart` returned a FALSE "timeout waiting
// for daemon to start" even though the daemon came up fine seconds later.
//
// The fix: while the daemon reports an *observably progressing* migration (a
// heartbeating status file written by internal/schema's migration reporter), the
// CLI shows a spinner and extends its wait instead of timing out. A genuinely
// hung daemon — no migration status at all, OR a migration whose heartbeat has
// frozen — STILL times out within a bounded window, so we never trade a
// false-fail for an infinite hang.

const (
	// defaultNoMigrationTimeout is the start-wait deadline when no migration is
	// in progress. Preserves the historical fixed 10s behavior for normal starts
	// and bounds a daemon that hangs before/without migrating.
	defaultNoMigrationTimeout = 10 * time.Second

	// defaultMigrationStallTimeout bounds the wait once a migration HAS been
	// observed: if the heartbeat stops advancing (and the daemon never becomes
	// ready) for this long, treat the migration as hung and time out. Also
	// covers the post-migration boot tail (the window resets when the migration
	// status file disappears), generous enough for the remaining startup work.
	defaultMigrationStallTimeout = 60 * time.Second

	defaultStartWaitPollInterval = 100 * time.Millisecond
)

// daemonStartWaitConfig parameterizes waitForDaemonReady. Production callers use
// daemonStartWaitDefaults; tests inject short timeouts and a silent spinner.
type daemonStartWaitConfig struct {
	socketPath         string
	wsPortPath         string
	varDir             string
	noMigrationTimeout time.Duration
	stallTimeout       time.Duration
	pollInterval       time.Duration
	spinner            *startWaitSpinner
}

// daemonStartWaitDefaults builds the production config for the given paths.
func daemonStartWaitDefaults(socketPath, wsPortPath, varDir string) daemonStartWaitConfig {
	return daemonStartWaitConfig{
		socketPath:         socketPath,
		wsPortPath:         wsPortPath,
		varDir:             varDir,
		noMigrationTimeout: defaultNoMigrationTimeout,
		stallTimeout:       defaultMigrationStallTimeout,
		pollInterval:       defaultStartWaitPollInterval,
		spinner:            newStartWaitSpinner(os.Stderr, isStderrTTY()),
	}
}

// waitForDaemonReady blocks until the daemon is ready (socket + ws.port both
// exist) or a bounded timeout fires. It surfaces migration progress via the
// spinner and refuses to false-timeout while a migration is making progress.
//
// Liveness model + residual: "progress" is the migration reporter's heartbeat,
// which advances on a wall-clock timer in the daemon process (see
// internal/schema migrationReporter). So the stall-timeout reliably bounds a
// DEAD daemon process (its heartbeat goroutine dies with it) and a daemon that
// never starts migrating. It does NOT bound the narrow case of a daemon process
// that stays alive while its migration goroutine deadlocks (e.g. an indefinite
// SQLite lock wait): the independent heartbeat keeps ticking and the wait would
// extend. That residual is accepted here — the reported and realistic failure
// is "migration is slow," not "migration deadlocks with a live process," and
// SQLite's busy_timeout already bounds most lock waits. Per-step migration
// progress (vs wall-clock heartbeat) would close it but is out of scope for a
// start-wait UX fix.
func waitForDaemonReady(cfg daemonStartWaitConfig) error {
	if cfg.pollInterval <= 0 {
		cfg.pollInterval = defaultStartWaitPollInterval
	}
	ticker := time.NewTicker(cfg.pollInterval)
	defer ticker.Stop()
	if cfg.spinner != nil {
		defer cfg.spinner.stop()
	}

	start := time.Now()
	lastProgress := start
	migrationSeen := false
	migrationFinished := false
	var lastHeartbeat int64 = -1

	for {
		<-ticker.C
		now := time.Now()

		// Ready check: both socket and ws.port present.
		if fileExists(cfg.socketPath) && fileExists(cfg.wsPortPath) {
			return nil
		}

		// Migration progress check. Distinguish a genuinely-absent status file
		// (st == nil, stErr == nil) from a transient read error (stErr != nil,
		// e.g. a half-written file mid-rename or a momentary EACCES): only a
		// confirmed-absent file marks the migration finished. On a read error we
		// keep prior state and retry on the next poll.
		st, stErr := schema.ReadMigrationStatus(cfg.varDir)
		switch {
		case st != nil:
			if !migrationSeen {
				migrationSeen = true
				lastProgress = now
			}
			if st.Heartbeat != lastHeartbeat {
				lastHeartbeat = st.Heartbeat
				lastProgress = now
			}
			if cfg.spinner != nil {
				cfg.spinner.update(fmt.Sprintf(
					"Migrating database schema v%d->v%d (%s) — this can take a while on large DBs",
					st.FromVersion, st.ToVersion, st.Phase))
			}
		case stErr == nil && migrationSeen && !migrationFinished:
			// Status file genuinely vanished: migration finished, daemon is
			// finishing startup. Reset the progress clock so the remaining boot
			// work gets a fresh stall window instead of inheriting migration
			// elapsed time.
			migrationFinished = true
			lastProgress = now
			if cfg.spinner != nil {
				cfg.spinner.update("Migration complete — finishing daemon startup")
			}
		}

		// Timeout decision.
		if migrationSeen {
			if now.Sub(lastProgress) > cfg.stallTimeout {
				if migrationFinished {
					// Migration succeeded; the hang is in the remaining boot work,
					// NOT the migration — say so, so the operator doesn't
					// re-investigate a migration that already completed.
					return fmt.Errorf("timeout waiting for daemon to start (migration completed but daemon did not become ready within %s)", cfg.stallTimeout)
				}
				return fmt.Errorf("timeout waiting for daemon to start (migration appears stalled — no progress for %s)", cfg.stallTimeout)
			}
		} else {
			if now.Sub(start) > cfg.noMigrationTimeout {
				return fmt.Errorf("timeout waiting for daemon to start")
			}
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isStderrTTY reports whether stderr is a terminal (so the spinner animates in
// place rather than spamming log lines).
func isStderrTTY() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// startWaitSpinner renders migration progress. On a TTY it animates a single
// in-place line (carriage-return rewrite); off a TTY it prints each distinct
// message once (so daemon-start logs still record that a migration ran).
type startWaitSpinner struct {
	w         io.Writer
	tty       bool
	frames    []rune
	frameIdx  int
	lastMsg   string
	started   bool
	maxLineLn int
}

func newStartWaitSpinner(w io.Writer, tty bool) *startWaitSpinner {
	return &startWaitSpinner{
		w:      w,
		tty:    tty,
		frames: []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'},
	}
}

// update sets the current message and advances the animation frame (TTY only).
func (s *startWaitSpinner) update(msg string) {
	if s == nil || s.w == nil {
		return
	}
	if s.tty {
		frame := s.frames[s.frameIdx%len(s.frames)]
		s.frameIdx++
		line := fmt.Sprintf("%c %s", frame, msg)
		// Track width in display columns (runes), not bytes — the Braille frame
		// glyphs are multi-byte runes, so a byte count would over/under-pad and
		// could leave stale characters from a longer previous line.
		cols := utf8.RuneCountInString(line)
		if cols > s.maxLineLn {
			s.maxLineLn = cols
		}
		// Carriage return + pad with trailing spaces to clear any longer
		// previous line (pad amount computed in display columns).
		_, _ = fmt.Fprint(s.w, "\r"+line+strings.Repeat(" ", s.maxLineLn-cols))
		s.started = true
		s.lastMsg = msg
		return
	}
	// Non-TTY: print each distinct message once.
	if msg != s.lastMsg {
		_, _ = fmt.Fprintln(s.w, msg)
		s.lastMsg = msg
		s.started = true
	}
}

// stop finalizes the spinner line (TTY: clear the animated line).
func (s *startWaitSpinner) stop() {
	if s == nil || s.w == nil || !s.started {
		return
	}
	if s.tty {
		// Clear the spinner line so it doesn't linger before normal output.
		_, _ = fmt.Fprint(s.w, "\r"+strings.Repeat(" ", s.maxLineLn)+"\r")
	}
}
