package guard

import "log/slog"

// WriterContext is the input to G4 (daemon writer liveness). Called
// by the daemon before any mutation of an identity file: we do not
// write on behalf of an agent whose process has exited.
type WriterContext struct {
	// Mode selects enforcement (strict/warn/off).
	Mode Mode

	// SubjectPID is the agent PID whose identity file the daemon is
	// about to mutate.
	SubjectPID int

	// OriginDaemon identifies the daemon that originated the write.
	// Empty string or "local" means the write was initiated on this
	// host; any other value is a cross-daemon mirror update and
	// must not be gated by local PID liveness (the subject PID is
	// valid on the origin host, not ours).
	OriginDaemon string

	// IsPIDAlive probes whether SubjectPID is still running. Nil
	// defaults to "assume alive" — conservative.
	IsPIDAlive func(int) bool

	// WarnLogger receives structured events in ModeWarn.
	WarnLogger *slog.Logger
}

// G4 refuses daemon-side writes targeting a dead agent. Closes the
// race where an agent crashes mid-write and a stale RPC lands on its
// identity file. Cross-daemon mirror writes are exempt.
func G4(wc *WriterContext) error {
	if wc.Mode == ModeOff {
		return nil
	}
	if wc.OriginDaemon != "" && wc.OriginDaemon != "local" {
		return nil // cross-daemon mirror: PID is valid on the origin host
	}
	if wc.IsPIDAlive == nil || wc.IsPIDAlive(wc.SubjectPID) {
		return nil
	}
	e := &Error{
		Guard:       "daemon_writer_liveness",
		Reason:      "subject_pid_dead",
		ExpectedPID: wc.SubjectPID,
		Remediation: "daemon refusing to write to dead agent's identity file",
	}
	if wc.Mode == ModeWarn {
		emitGuardFire(wc.WarnLogger, wc.Mode, "allowed", e)
		return nil
	}
	emitGuardFire(wc.WarnLogger, wc.Mode, "denied", e)
	return e
}
