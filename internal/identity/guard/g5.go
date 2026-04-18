package guard

import (
	"fmt"
	"log/slog"
	"os"
)

// PrimeContext is the input to G5 (prime ownership). Assembled by the
// CLI's prime command from the caller's ancestor chain + identity
// file path + liveness probe.
type PrimeContext struct {
	// Mode selects enforcement (strict/warn/off).
	Mode Mode

	// IdentityPath is the absolute path to the identity file being
	// primed (or reclaimed). Empty or non-existent path is a
	// healthy first-prime state.
	IdentityPath string

	// ClosestRtPID is the PID of the caller's closest runtime
	// ancestor along the chain. Callers MUST populate this by
	// invoking guard.ClosestRuntimeAncestor(ctx, os.Getpid()) —
	// passing a stale or wrong PID here silently defeats the
	// ownership check. A dedicated wire-up wrapper is landing in
	// Epic 4 (thrum-25gf) so CLI callers do not have to remember
	// this discipline.
	ClosestRtPID int

	// IsPIDAlive probes whether a PID is still running. Nil
	// defaults to "assume alive" — conservative.
	//
	// Same PID-recycling caveat as rule.go:IsPIDAlive applies: a
	// single probe cannot distinguish "original owner exited" from
	// "original owner's PID was reassigned to a fresh process." G5
	// relies on the AgentPID==ClosestRtPID match being the
	// exceptional path, not the fallthrough, to contain that risk;
	// a secondary probe is tracked as future hardening.
	IsPIDAlive func(int) bool

	// WarnLogger receives structured events in ModeWarn.
	WarnLogger *slog.Logger
}

// G5 refuses prime when the caller is not the topmost runtime that
// owns the identity file. The canonical failure mode: a Claude Code
// sub-agent invokes `thrum prime` from inside a tool call; the
// sub-agent's closest runtime ancestor is still the parent Claude
// process, but the identity file already records the parent Claude's
// PID. If we let the sub-agent's ancestor-chain match, we'd be fine —
// but if somehow the sub-agent's closest runtime PID differs from the
// owner's recorded PID (nested runtimes, foreign agent, wrapping
// supervisor), only the topmost runtime has the right to re-prime.
//
// Absent / dead owners are pass-through: no file means nothing to
// protect yet; a dead owner is reclaimable like Rule #4‴ step 3.3.
func G5(pc *PrimeContext) error {
	if pc.Mode == ModeOff {
		return nil
	}
	id, err := loadIdentityPID(pc.IdentityPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to protect yet
		}
		return fmt.Errorf("load %s: %w", pc.IdentityPath, err)
	}
	// Dead owner — caller may reclaim.
	if pc.IsPIDAlive == nil || !pc.IsPIDAlive(id.AgentPID) {
		return nil
	}
	// Caller IS the topmost runtime — legitimate re-prime.
	if id.AgentPID == pc.ClosestRtPID {
		return nil
	}
	e := &Error{
		Guard:       "prime_ownership",
		Reason:      "caller_not_topmost_runtime",
		CallerPID:   pc.ClosestRtPID,
		ExpectedPID: id.AgentPID,
		Remediation: "you appear to be running inside a sub-agent; the parent runtime owns this identity — run prime from the top-level runtime instead",
	}
	if pc.Mode == ModeWarn {
		if pc.WarnLogger != nil {
			pc.WarnLogger.Warn("identity_guard_fire",
				"guard", e.Guard,
				"reason", e.Reason,
				"expected_pid", e.ExpectedPID,
				"caller_pid", e.CallerPID,
			)
		}
		return nil
	}
	return e
}
