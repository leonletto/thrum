package guard

import (
	"context"
	"fmt"
	"log/slog"
)

// CheckContext is the input to Rule and every companion guard. It is
// assembled by the caller — the CLI for advisory pre-flight or the
// daemon for authoritative RPC-time checks — from the running process
// state, the identity file on disk, and a few environment snapshots
// (TMUX var, CWD). The struct stays exported so outside callers can
// populate it inline; the lowercase fields are strictly test
// observability hooks and are invisible to production code paths.
type CheckContext struct {
	// Ctx carries deadlines and cancellation; primarily consumed by
	// any ps-shelling lookups the guards perform internally.
	Ctx context.Context

	// Mode selects the enforcement level for Rule #4‴ itself
	// (cross_worktree). Companion guards read their own modes off
	// the caller's Config and do not consult this field.
	Mode Mode

	// DeadReclaimMode gates step 3.3's auto-reclaim path
	// independently from Mode, so operators can turn reclaim off
	// while leaving the cross_worktree check strict. When
	// ModeOff, step 3.3 is skipped and dead-PID mismatches fall
	// through to step 3.4's strict-or-warn handling. When
	// ModeWarn, the reclaim still fires but the event is logged.
	// When ModeStrict (the default), reclaim is silent on
	// success.
	DeadReclaimMode Mode

	// Chain is the caller's process ancestor chain (self first,
	// walking up toward PID 1). Empty chain means "unknown caller"
	// — Rule treats that as the no-runtime case.
	Chain []int

	// ClosestRtPID is the PID of the nearest AI-runtime ancestor
	// along the chain, or 0 if none was found (bare shell, cron,
	// script). Zero short-circuits Rule to the step-4 passthrough.
	ClosestRtPID int

	// IdentityAgentPID is the PID stamped in the identity file at
	// registration time. Zero means "not recorded" (runtime without
	// PID exposure, legacy pre-guard file, or deliberately blank).
	IdentityAgentPID int

	// IsPIDAlive is an injectable liveness probe. Set to a real
	// process.IsRunning wrapper in production; tests inject a stub.
	// Nil defaults to "assume alive" — the conservative branch, so
	// a missing probe does not silently unlock auto-reclaim.
	//
	// Single-source liveness + PID recycling: kernel PIDs wrap, so a
	// just-died agent's PID could theoretically be reassigned to a
	// new unrelated process by the time step 3.3 fires. The
	// CWD+TMUX match clause is the existing guard against this; a
	// secondary liveness probe (procfs read, kqueue watcher) is
	// tracked as a future hardening task (spec
	// self-heal-cross-verify; see memory/project_state.md).
	IsPIDAlive func(int) bool

	// CWDMatches is true when the caller's working directory
	// resolves to the same worktree the identity file was written
	// for. False when the caller cd'd into a different worktree.
	// Callers MUST realpath-canonicalize the compared paths before
	// setting this field — symlink farms and worktree redirects
	// produce string-level drift that would otherwise mis-flag
	// legitimate owners (spec §Rule #4‴).
	CWDMatches bool

	// IdentitiesDir is the absolute path to .thrum/identities, used by
	// Rule's error-construction path to resolve DetectedAgent (the
	// agent the caller's ancestor chain actually belongs to) via
	// findOwnedIdentity. Empty when unknown or no identity files
	// exist; DetectedAgent then stays blank, which is fine — the
	// Error formatter elides blank optional fields.
	IdentitiesDir string

	// ExpectedAgent is the name recorded in the identity file the
	// check is protecting. Populated by buildCheckContext; Rule's
	// error path copies it into Error.ExpectedAgent.
	ExpectedAgent string

	// TmuxMatches is true when:
	//   - the TMUX env var is present on both the caller and the
	//     identity file, and the two values are equal, OR
	//   - TMUX is absent on both sides.
	// A one-sided presence counts as a mismatch — tmux sessions
	// pin identity; a stray shell from a different pane is not the
	// same agent.
	TmuxMatches bool

	// reclaim is the side-effect hook invoked on the dead-PID
	// reclaim path (step 3.3). Production wiring routes this
	// through guard.WritePID; tests stub it to assert the reclaim
	// fired (or, via withReclaimFails, to assert error propagation).
	reclaim func() error

	// warnLogger receives structured events when a guard fires in
	// ModeWarn. Nil warnLogger silently swallows warnings — the
	// daemon wires a real slog.Logger in Phase 3 (Epic 6).
	warnLogger *slog.Logger

	// reclaimedPtr and warnLoggedPtr are test observability hooks:
	// Rule flips the pointees when the corresponding side effect
	// runs. Production callers leave them nil.
	reclaimedPtr  *bool
	warnLoggedPtr *bool
}

// Rule implements the core cross_worktree guard (spec "Rule #4‴").
// The 4 steps + 3 sub-clauses map to the spec design doc:
//
//	Step 1  — mode gate (ModeOff = no-op)
//	Step 2  — runtime-ancestor detection
//	Step 3.1 — identity PID appears in caller's chain → proceed
//	Step 3.2 — identity PID == 0, CWD+TMUX both match → proceed
//	Step 3.3 — identity PID is dead, CWD+TMUX match → reclaim
//	Step 3.4 — hard error (strict) or structured warn log (warn)
//	Step 4  — no runtime ancestor → passthrough (human or script)
func Rule(cc *CheckContext) error {
	// Step 1: mode gate — off disables the guard entirely.
	if cc.Mode == ModeOff {
		return nil
	}

	// Step 2 + 4: no runtime ancestor → passthrough. The caller is a
	// human shell, a cron job, or some other non-runtime context
	// where identity ownership does not apply.
	if cc.ClosestRtPID == 0 {
		return nil
	}

	// Step 3.1: identity PID found in caller's ancestor chain. This
	// is the canonical healthy path (own worktree, own agent).
	if cc.IdentityAgentPID != 0 && ChainContains(cc.Chain, cc.IdentityAgentPID) {
		return nil
	}

	// Step 3.2: identity file records no PID (runtime without PID
	// exposure). Accept if CWD + TMUX both corroborate ownership.
	if cc.IdentityAgentPID == 0 {
		if cc.CWDMatches && cc.TmuxMatches {
			return nil
		}
	}

	// Step 3.3: recorded PID is dead. Treat as the original agent
	// having exited; let the new caller reclaim the identity file
	// iff CWD + TMUX agree the caller is the same logical session.
	// DeadReclaimMode gates this path — ModeOff falls through to
	// 3.4 (strict/warn decide); ModeWarn logs and reclaims;
	// ModeStrict (the default when unset) reclaims silently.
	if cc.DeadReclaimMode != ModeOff &&
		cc.IdentityAgentPID != 0 &&
		cc.IsPIDAlive != nil &&
		!cc.IsPIDAlive(cc.IdentityAgentPID) &&
		cc.CWDMatches && cc.TmuxMatches {
		if cc.reclaim != nil {
			if err := cc.reclaim(); err != nil {
				// Propagate: a failed reclaim must not be
				// masked as "allow." Disk full / lock
				// contention / perms are operational errors
				// the caller needs to see.
				return fmt.Errorf("auto-reclaim: %w", err)
			}
			if cc.reclaimedPtr != nil {
				*cc.reclaimedPtr = true
			}
		}
		emitGuardFire(cc.warnLogger, cc.DeadReclaimMode, "auto_reclaimed", &Error{
			Guard:       "dead_pid_auto_reclaim",
			Reason:      "dead_owner_reclaimed",
			ExpectedPID: cc.IdentityAgentPID,
			CallerPID:   chainHead(cc.Chain),
		})
		return nil
	}

	// Step 3.4: no rescue clause matched. Either hard error
	// (strict) or emit a structured warn and proceed (warn).
	detected := ""
	if cc.IdentitiesDir != "" {
		if name, _, _ := findOwnedIdentity(cc.IdentitiesDir, cc.Chain); name != "" {
			detected = name
		}
	}
	e := &Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		CallerPID:     chainHead(cc.Chain),
		ExpectedAgent: cc.ExpectedAgent,
		DetectedAgent: detected,
		ExpectedPID:   cc.IdentityAgentPID,
		Remediation:   "cd to the correct worktree, run 'thrum prime' to re-claim, or pass --repo <path> to anchor to a specific repo",
	}
	if cc.Mode == ModeWarn {
		emitGuardFire(cc.warnLogger, cc.Mode, "allowed", e)
		if cc.warnLoggedPtr != nil {
			*cc.warnLoggedPtr = true
		}
		return nil
	}
	emitGuardFire(cc.warnLogger, cc.Mode, "denied", e)
	return e
}

// chainHead returns the caller PID (the chain's first element) or 0
// when the chain is empty — that zero is semantically "caller PID
// unknown," which is what Error's zero-rendering convention expects.
func chainHead(c []int) int {
	if len(c) == 0 {
		return 0
	}
	return c[0]
}
