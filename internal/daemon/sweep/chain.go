package sweep

import (
	"context"
	"fmt"
	"strings"
)

// ChainConfig is the minimal input ChainResolver needs. Decoupled
// from internal/config so this task can land before .16 fixes the
// canonical config block shape — production wiring at .16 plucks
// values out of config.DaemonConfig and constructs this struct.
//
// Per canonical §4.4:
//   - AlertChain: optional override (set via daemon.sweep.alert_chain
//     JSON array). When non-empty, fully replaces the supervisor
//     fallback.
//   - SupervisorAgentName: from escalation.supervisor_agent_name with
//     canonical default "coordinator". Used when AlertChain is empty.
type ChainConfig struct {
	AlertChain          []string
	SupervisorAgentName string
}

// defaultSupervisorAgentName is the canonical §4.4 default for
// escalation.supervisor_agent_name when nothing is configured.
const defaultSupervisorAgentName = "coordinator"

// ChainResolverImpl satisfies sweep.ChainResolver via the
// config-first + supervisor-agent-fallback rule. Per cycle-2 finding
// #3 + architect-landed §4.4 (2026-05-15) the resolver is two-rung:
//
//  1. If daemon.sweep.alert_chain is non-empty → return it verbatim.
//     Operators set this when they want a multi-recipient chain like
//     ["@coordinator_main", "leon@example.com"] (agent + email).
//
//  2. Otherwise → return [@supervisor_agent_name] (single agent
//     fallback). The supervisor name has a canonical default of
//     "coordinator", so the fallback always yields at least one
//     recipient — chain is never empty.
//
// The brainstorm originally proposed a two-element fallback
// [coordinator, user-email], but canonical §4.4 simplified to single-
// supervisor; operators wanting multi-recipient configure
// AlertChain explicitly.
type ChainResolverImpl struct {
	cfg ChainConfig
}

// NewChainResolver builds the resolver from a config snapshot.
// Production callers extract AlertChain + SupervisorAgentName from
// the daemon config; tests pass the struct directly.
func NewChainResolver(cfg ChainConfig) *ChainResolverImpl {
	return &ChainResolverImpl{cfg: cfg}
}

// Resolve returns the delivery chain per the rules above. Error
// return is preserved for the ChainResolver interface contract even
// though the current logic can't fail — future config validation
// (e.g. rejecting empty strings in AlertChain) may surface errors
// without requiring an interface change.
func (r *ChainResolverImpl) Resolve(_ context.Context) ([]string, error) {
	if len(r.cfg.AlertChain) > 0 {
		// Copy the slice so callers can't mutate the config's
		// underlying backing array.
		out := make([]string, len(r.cfg.AlertChain))
		copy(out, r.cfg.AlertChain)
		return out, nil
	}
	sup := r.cfg.SupervisorAgentName
	if sup == "" {
		sup = defaultSupervisorAgentName
	}
	if !strings.HasPrefix(sup, "@") {
		sup = "@" + sup
	}
	return []string{sup}, nil
}

// Compile-time satisfaction check.
var _ ChainResolver = (*ChainResolverImpl)(nil)

// ValidateChainConfig is a daemon-boot guard against a misconfiguration
// that would cause the dispatcher to infinite-loop on every fire tick.
//
// Setup that triggers the loop:
//   1. AlertChain configured with ONLY email entries (no @agent refs).
//   2. EmailQueue NOT wired — either D-B1 substrate absent (pre-v0.11)
//      or thrumCfg.Email.Enabled=false (bridge disabled at runtime;
//      worker doesn't drain the queue table).
//
// Symptom without this guard:
//   - DeliverySink.fanToChain processes the chain
//   - Every email entry is skipped (EmailQueue is nil)
//   - delivered=0, no errors → returns "no recipients reached (all
//     skipped)" error
//   - Dispatcher leaves the row state=open (per at-least-once
//     semantics)
//   - Next tick (30s later): same chain, same skip, same error,
//     same retry — for the lifetime of the daemon.
//
// Resolution: validate at daemon boot. If AlertChain is non-empty and
// contains only email entries while email delivery isn't wired,
// return a clear error so the operator fixes config rather than ships
// a daemon that infinite-loops.
//
// Three pass-through paths (no error returned):
//   - hasEmailDelivery=true (email bridge enabled; queue worker
//     drains email_outbound_queue and emails actually deliver).
//   - AlertChain has at least one @agent entry (mixed chain — agent
//     entries deliver, email entries log+skip, fire succeeds).
//   - AlertChain is empty (resolver falls back to single supervisor,
//     never email-only).
//
// The guard remains permanent: hasEmailDelivery is tied to
// thrumCfg.Email.Enabled at the composition root, so even with the
// D-B1 substrate present a disabled bridge still triggers the
// infinite-loop scenario and must be rejected at boot.
func ValidateChainConfig(cfg ChainConfig, hasEmailDelivery bool) error {
	if hasEmailDelivery {
		return nil
	}
	if len(cfg.AlertChain) == 0 {
		// Fallback path uses single supervisor — never email-only.
		return nil
	}
	for _, entry := range cfg.AlertChain {
		if isAgentRef(entry) {
			// At least one @agent entry — mixed chain delivers via
			// MessageSender even if email entries skip.
			return nil
		}
	}
	return fmt.Errorf("daemon.sweep.alert_chain contains only email entries (%v) "+
		"but email delivery is not wired; either configure at least one @agent entry "+
		"or unset daemon.sweep.alert_chain to fall back to "+
		"escalation.supervisor_agent_name", cfg.AlertChain)
}

// isAgentRef recognizes "@agent_name" entries in target_chain.
// Mirrors DeliverySink.isAgentRef (internal/daemon/reminders/delivery.go)
// — sweep + reminders agree on chain-entry shape detection.
func isAgentRef(entry string) bool {
	return strings.HasPrefix(entry, "@") && len(entry) > 1
}
