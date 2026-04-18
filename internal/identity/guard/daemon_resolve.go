package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/process"
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

	// ConnectingPID is the kernel-verified PID of the connecting
	// process (from SO_PEERCRED / LOCAL_PEERPID). Used for Rule #4‴
	// ancestor-chain authentication on the anonymous-peercred branch:
	// when CWD-based matching failed, the chain walk tries to find a
	// registered agent whose AgentPID appears in ConnectingPID's
	// ancestor chain AND is still alive. Zero disables the chain walk
	// (tests, non-unix transports).
	ConnectingPID int

	// IdentitiesDir is the absolute path to the `.thrum/identities`
	// directory the chain walk should enumerate. Typically
	// <state.RepoPath>/.thrum/identities. Empty disables the chain
	// walk even when ConnectingPID is set.
	IdentitiesDir string
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
func DaemonResolve(ctx context.Context, cfg Config, req DaemonResolveRequest, logger *slog.Logger) (ResolvedCaller, error) {
	if req.PeercredRan {
		if req.PeercredAgentID != "" {
			if req.CallerAgentID != "" && req.CallerAgentID != req.PeercredAgentID {
				e := &Error{
					Guard:  "unauthenticated_rpc",
					Reason: "identity_mismatch",
					Remediation: fmt.Sprintf(
						"caller claimed %q but unix-socket peer credentials resolve to %q",
						req.CallerAgentID, req.PeercredAgentID,
					),
				}
				// Forgery rejection is unconditional (fires regardless
				// of mode); emit as denied so dashboards can alert on
				// mismatch attempts even when other guards are off.
				emitGuardFire(logger, cfg.UnauthenticatedRPC, "denied", e)
				return ResolvedCaller{}, e
			}
			return ResolvedCaller{AgentID: req.PeercredAgentID}, nil
		}
		// Anonymous peercred: try Rule #4‴ ancestor-chain walk before
		// failing closed. A matching identity file whose AgentPID
		// lives in the caller's chain AND is still alive authenticates
		// the caller. Dead matches fall through — self-heal cross-
		// verify: liveness must corroborate the file's claim.
		if agentID := resolveByChain(ctx, req); agentID != "" {
			if req.CallerAgentID != "" && req.CallerAgentID != agentID {
				e := &Error{
					Guard:  "unauthenticated_rpc",
					Reason: "identity_mismatch",
					Remediation: fmt.Sprintf(
						"caller claimed %q but ancestor-chain walk resolves to %q",
						req.CallerAgentID, agentID,
					),
				}
				emitGuardFire(logger, cfg.UnauthenticatedRPC, "denied", e)
				return ResolvedCaller{}, e
			}
			return ResolvedCaller{AgentID: agentID}, nil
		}
		e := &Error{
			Guard:       "unauthenticated_rpc",
			Reason:      "anonymous_mutating_rpc",
			Remediation: "cd into a registered agent worktree and retry",
		}
		emitGuardFire(logger, cfg.UnauthenticatedRPC, "denied", e)
		return ResolvedCaller{}, e
	}
	if err := G3(cfg.UnauthenticatedRPC, req.CallerAgentID, logger); err != nil {
		return ResolvedCaller{}, err
	}
	return ResolvedCaller{AgentID: req.CallerAgentID}, nil
}

// resolveByChain walks ConnectingPID's ancestor chain and returns the
// agent_id of the first registered identity whose AgentPID appears in
// the chain AND is still alive. Returns "" when the walk is disabled
// (zero PID or empty IdentitiesDir), fails, or produces no live
// match. Stale identities (AgentPID in chain but dead) do NOT
// authenticate — self-heal cross-verify: the DB-only PID self-heal
// incident (thrum-pxz.14) demonstrates why single-source stale checks
// false-positive.
func resolveByChain(ctx context.Context, req DaemonResolveRequest) string {
	if req.ConnectingPID <= 0 || req.IdentitiesDir == "" {
		return ""
	}
	chain, err := WalkAncestors(ctx, req.ConnectingPID)
	if err != nil || len(chain) == 0 {
		return ""
	}
	entries, err := os.ReadDir(req.IdentitiesDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// Skip .deleted sidekicks (retired identities).
		if strings.HasSuffix(entry.Name(), ".deleted.json") {
			continue
		}
		path := filepath.Join(req.IdentitiesDir, entry.Name())
		// #nosec G304 -- path constrained to the caller-supplied
		// identities dir, not external input.
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var id struct {
			Agent struct {
				Name string `json:"name"`
			} `json:"agent"`
			AgentPID int `json:"agent_pid"`
		}
		if err := json.Unmarshal(data, &id); err != nil {
			continue
		}
		if id.AgentPID == 0 || !ChainContains(chain, id.AgentPID) {
			continue
		}
		// Self-heal cross-verify: a stale identity file's AgentPID
		// may appear in a descendant's chain by accident; require
		// liveness before trusting it.
		if !process.IsRunning(id.AgentPID) {
			continue
		}
		return id.Agent.Name
	}
	return ""
}
