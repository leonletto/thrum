package guard

import "log/slog"

// emitGuardFire emits a single structured slog event for a guard
// outcome. Called from every guard's fire path so strict-mode
// rejections get the same observability as warn-mode allowances;
// the shape is unified so operators can alert on `guard="..." +
// outcome="denied"` regardless of which guard fired.
//
// Outcome vocabulary:
//   - "denied"         — strict-mode refusal (caller gets the *Error)
//   - "allowed"        — warn-mode fall-through (would have denied in strict)
//   - "auto_reclaimed" — Rule #4‴ step 3.3 dead-PID reclaim
//   - "skipped"        — guard precondition not met so enforcement did not
//     apply (e.g. G4 cross-daemon mirror write — the subject PID is
//     valid on the origin host, not ours, so local liveness doesn't
//     apply). Distinct from "allowed": no fire would have happened
//     under any mode.
//
// Attribute fields derive from the *Error being reported plus the
// resolved mode + outcome. Zero-valued optional fields are omitted.
// A nil logger is a silent no-op so callers don't need their own
// nil checks. The event name "identity_guard_fire" is a stable
// contract — operators bind log/telemetry pipelines to it.
func emitGuardFire(logger *slog.Logger, mode Mode, outcome string, e *Error) {
	if logger == nil || e == nil {
		return
	}
	attrs := []any{
		"guard", e.Guard,
		"mode", string(mode),
		"outcome", outcome,
		"reason", e.Reason,
	}
	if e.CallerPID != 0 {
		attrs = append(attrs, "caller_pid", e.CallerPID)
	}
	if e.CallerCWD != "" {
		attrs = append(attrs, "caller_cwd", e.CallerCWD)
	}
	if e.ExpectedAgent != "" {
		attrs = append(attrs, "expected_agent", e.ExpectedAgent)
	}
	if e.DetectedAgent != "" {
		attrs = append(attrs, "detected_agent", e.DetectedAgent)
	}
	if e.ExpectedPID != 0 {
		attrs = append(attrs, "expected_pid", e.ExpectedPID)
	}
	logger.Warn("identity_guard_fire", attrs...)
}
