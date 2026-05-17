package sweep

import (
	"context"
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
