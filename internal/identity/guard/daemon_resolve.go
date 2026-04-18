package guard

import (
	"fmt"
	"log/slog"
)

// DaemonResolveRequest carries the inputs DaemonResolve needs to
// authenticate an incoming RPC. All fields are populated by the daemon's
// accept loop / request dispatcher:
//
//   - CallerAgentID comes from the RPC frame (client-asserted, never
//     trusted on its own).
//   - PeercredAgentID is the kernel-verified identity from peercred.Resolve
//     (empty when peercred ran but produced no match, i.e. anonymous).
//   - PeercredRan reports whether the accept loop attempted peercred at
//     all. It is distinct from "PeercredAgentID != \"\"" because
//     peercred-ran-and-anonymous is a policy-relevant state: the caller
//     connected over a unix socket but is not in any registered agent
//     worktree, which must fail closed on mutating RPCs.
type DaemonResolveRequest struct {
	CallerAgentID   string
	PeercredAgentID string
	PeercredRan     bool
}

// ResolvedCaller is DaemonResolve's authoritative answer: the agent ID
// the handler should act on. When peercred resolved a trusted identity,
// this is the kernel-verified ID; otherwise it is the G3-approved
// client claim.
type ResolvedCaller struct {
	AgentID string
}

// DaemonResolve is the authoritative daemon-side identity check. Every
// mutating RPC handler must call DaemonResolve instead of falling back
// to ad-hoc identity derivation ("load config and pick whatever
// agent_id the daemon's repo config names").
//
// Semantics, in priority order:
//
//  1. peercred resolved a trusted identity → use it. If the caller also
//     asserted a mismatched CallerAgentID, return an identity_mismatch
//     *Error regardless of config mode — forgery defense is unconditional.
//  2. peercred ran but produced no match (anonymous) → return an
//     anonymous_mutating_rpc *Error. Mutating handlers must not run for
//     anonymous callers; the read-only allowlist is enforced by the
//     dispatcher before the handler fires, so reaching here is already
//     a policy violation on that side.
//  3. peercred did not run (tests, non-unix transport) → delegate to
//     G3 on the CallerAgentID claim. Strict mode fails closed on empty;
//     warn mode logs-and-continues (preserves legacy behavior); off
//     mode silently bypasses. When G3 passes, the CallerAgentID claim
//     is returned verbatim — this is the legacy "trust the claim" path
//     kept alive for tests and non-unix transports (browser/WS).
//
// The logger receives structured warn-mode events when G3 fires; nil
// is tolerated. DaemonResolve never logs trusted-path or mismatch
// events — those are handler/dispatcher concerns.
func DaemonResolve(cfg Config, req DaemonResolveRequest, logger *slog.Logger) (ResolvedCaller, error) {
	if req.PeercredRan {
		if req.PeercredAgentID != "" {
			if req.CallerAgentID != "" && req.CallerAgentID != req.PeercredAgentID {
				return ResolvedCaller{}, &Error{
					Guard:  "unauthenticated_rpc",
					Reason: "identity_mismatch",
					Remediation: fmt.Sprintf(
						"caller claimed %q but unix-socket peer credentials resolve to %q",
						req.CallerAgentID, req.PeercredAgentID,
					),
				}
			}
			return ResolvedCaller{AgentID: req.PeercredAgentID}, nil
		}
		return ResolvedCaller{}, &Error{
			Guard:       "unauthenticated_rpc",
			Reason:      "anonymous_mutating_rpc",
			Remediation: "cd into a registered agent worktree and retry",
		}
	}
	if err := G3(cfg.UnauthenticatedRPC, req.CallerAgentID, logger); err != nil {
		return ResolvedCaller{}, err
	}
	return ResolvedCaller{AgentID: req.CallerAgentID}, nil
}
