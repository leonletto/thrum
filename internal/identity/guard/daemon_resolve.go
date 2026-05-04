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

	// PeercredWorktree is the absolute worktree path that peercred
	// resolved for the caller's PID (empty when peercred is
	// anonymous or did not run). Used by IsAgentInWorktree below to
	// disambiguate callers in shared worktrees.
	PeercredWorktree string

	// IsAgentInWorktree reports whether the given agent_id was ever
	// registered in the given worktree — the concrete implementation
	// in state.IsAgentInWorktree looks at historical session_refs
	// (no ended_at IS NULL filter) plus the identity-file fallback.
	// Active-session filtering is deliberately not applied: an agent
	// whose session is temporarily ended (between session.end and the
	// next session.start) is still a legitimate co-located agent, and
	// an agent that just ran `thrum agent register` has no session_ref
	// yet but does have a .thrum/identities/<name>.json at the
	// peercred-verified worktree.
	//
	// When set AND the caller's claim mismatches the peercred-resolved
	// agent AND the claimed agent satisfies this predicate for the
	// peercred-resolved worktree, DaemonResolve trusts the claim. This
	// is the thrum-0pos shared-worktree disambiguation fallback:
	// peercred cannot distinguish multiple agents co-located in the
	// same worktree (E2E harnesses, multi-agent test scenarios), so a
	// matching CLI claim is the only reliable signal the daemon has.
	// Nil disables the fallback and preserves strict per-CWD
	// enforcement (tests, non-unix transports).
	IsAgentInWorktree func(agentID, worktree string) bool
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
// Warn-mode tightening on peercred-anonymous: when peercred ran AND
// the chain walk produced no live match, we hard-deny regardless of
// the UnauthenticatedRPC mode. This is a deliberate deviation from
// G3 warn's "log-and-continue" semantics — kernel-level evidence
// that the caller is unregistered is stronger than an absent
// CallerAgentID on a non-peercred transport, so the warn fallback
// does not extend here. G3's warn-mode fallback still applies on
// step 3 above (non-peercred paths).
//
// The logger receives structured warn-mode events when G3 fires; nil
// is tolerated. DaemonResolve never logs trusted-path or mismatch
// events — those are handler/dispatcher concerns.
func DaemonResolve(ctx context.Context, cfg Config, req DaemonResolveRequest, logger *slog.Logger) (ResolvedCaller, error) {
	if req.PeercredRan {
		if req.PeercredAgentID != "" {
			if req.CallerAgentID != "" && req.CallerAgentID != req.PeercredAgentID {
				// thrum-0pos shared-worktree fallback: peercred
				// cannot disambiguate multiple agents co-located in
				// the same worktree (E2E harnesses, multi-agent test
				// scenarios, peer-bridge proxies). If the CLI's
				// claim is ALSO a registered agent in the same
				// worktree that peercred resolved, trust the claim —
				// the caller is one of the co-located agents and
				// kernel-verified CWD corroborates that membership.
				// Cross-worktree impersonation (claim from another
				// worktree) still falls through to the mismatch
				// error below, preserving the forgery defense.
				if req.IsAgentInWorktree != nil && req.PeercredWorktree != "" &&
					req.IsAgentInWorktree(req.CallerAgentID, req.PeercredWorktree) {
					return ResolvedCaller{AgentID: req.CallerAgentID}, nil
				}
				e := &Error{
					Guard:         "unauthenticated_rpc",
					Reason:        "identity_mismatch",
					ExpectedAgent: req.CallerAgentID,
					DetectedAgent: req.PeercredAgentID,
					Remediation: fmt.Sprintf(
						"your current directory is inside %q's worktree. cd into %q's worktree and retry, or drop the identity claim to act as %q.",
						req.PeercredAgentID, req.CallerAgentID, req.PeercredAgentID,
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
					Guard:         "unauthenticated_rpc",
					Reason:        "identity_mismatch",
					ExpectedAgent: req.CallerAgentID,
					DetectedAgent: agentID,
					Remediation: fmt.Sprintf(
						"your process ancestor chain belongs to %q. retry from %q's worktree or tmux pane, or drop the identity claim to act as %q.",
						agentID, req.CallerAgentID, agentID,
					),
				}
				emitGuardFire(logger, cfg.UnauthenticatedRPC, "denied", e)
				return ResolvedCaller{}, e
			}
			return ResolvedCaller{AgentID: agentID}, nil
		}
		e := &Error{
			Guard:  "unauthenticated_rpc",
			Reason: "anonymous_mutating_rpc",
			// thrum-8nro.3: the prior remediation ("cd into a registered
			// agent worktree") was misleading when the caller WAS in a
			// registered worktree but the daemon's binding cache hadn't
			// been warmed (post-restart, post-edit, etc.). 'thrum prime'
			// is the actual recovery in that case.
			Remediation: "daemon hasn't bound this caller to a registered agent. If you ARE a registered agent in this worktree, run 'thrum prime' to warm the daemon's binding cache, then retry. Otherwise cd into the agent's worktree first.",
		}
		emitGuardFire(logger, cfg.UnauthenticatedRPC, "denied", e)
		return ResolvedCaller{}, e
	}
	if err := G3(cfg.UnauthenticatedRPC, req.CallerAgentID, logger); err != nil {
		return ResolvedCaller{}, err
	}
	return ResolvedCaller{AgentID: req.CallerAgentID}, nil
}

// closestRuntimeAncestorFn is the runtime-ancestor probe used by
// resolveByChain. Exposed as a var so tests in this package can substitute
// a deterministic stub on platforms where the test process has no real
// AI-runtime ancestor in its chain (e.g. fresh GH Actions runners).
// Production code MUST NOT mutate this.
var closestRuntimeAncestorFn = ClosestRuntimeAncestor

// resolveByChain walks ConnectingPID's ancestor chain and returns the
// agent_id of the first registered identity whose AgentPID appears in
// the chain AND is still alive. Returns "" when the walk is disabled
// (zero PID or empty IdentitiesDir), fails, lacks a runtime ancestor,
// or produces no live match.
//
// The runtime-ancestor precondition enforces spec §Rule #4‴ step 2:
// identity authentication only applies to callers running under a
// recognized AI runtime (claude, codex, kiro, etc.). A bare shell,
// cron job, or script with no runtime ancestor must fall through to
// anonymous — they are legitimately unrelated to any agent session.
//
// Stale identities (AgentPID in chain but dead) do NOT authenticate:
// a PID-only liveness check false-positives on stale files; cross-
// verifying chain-membership with process-liveness is the minimum
// trustworthy combination.
func resolveByChain(ctx context.Context, req DaemonResolveRequest) string {
	if req.ConnectingPID <= 0 || req.IdentitiesDir == "" {
		return ""
	}
	// Spec §Rule #4‴ step 2: identity authentication applies only
	// when the caller has a recognized runtime in its ancestor chain.
	// Non-runtime callers (bare shell / cron / script) fall through
	// to the anonymous-allow or anonymous-deny path per policy.
	if rtPID, _, _ := closestRuntimeAncestorFn(ctx, req.ConnectingPID); rtPID == 0 {
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
		// A PID-only liveness check false-positives on stale files;
		// see the self-heal cross-verify design notes. Chain-membership
		// plus process-liveness is the minimum trustworthy combination
		// before declaring the chain match authentic.
		if !process.IsRunning(id.AgentPID) {
			continue
		}
		return id.Agent.Name
	}
	return ""
}
