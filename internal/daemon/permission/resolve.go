package permission

import (
	"context"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// stateQuery is the minimal interface resolveSupervisors needs from
// *state.State. Extracted so tests can supply a fake without spinning
// up a full SQLite instance.
type stateQuery interface {
	IsAgentActive(ctx context.Context, name string) (bool, error)
	ListActiveAgentsByRole(ctx context.Context, role string) ([]string, error)
}

// Compile-time assertion: *state.State must satisfy stateQuery.
var _ stateQuery = (*state.State)(nil)

// ResolveSupervisors parses the config permission_supervisors array
// into concrete @-prefixed recipient IDs. Each entry is one of:
//
//   - a role name ("coordinator", "orchestrator"): broadcasts to every
//     active agent currently registered under that role.
//   - a specific agent name ("@coordinator_main"): direct delivery if
//     active, silently skipped if offline.
//   - a specific user ("@user:leon-letto"): same as a named agent —
//     the user must have an active agent session.
//
// Returns a deduplicated slice of @-prefixed recipient IDs. When
// `entries` is empty or nil, defaults to ["coordinator"].
//
// Locality (thrum-x3fnh, the wo2z SOURCE-2 facet): agent recipients without
// a LOCAL identity file are skipped. The agents table contains synced REMOTE
// agents (ListActiveAgentsByRole has no hostname filter), and fanning
// supervisor traffic to a remote coordinator produces non-actionable nudges
// on hosts that hold neither the pane nor the nudge row — the all-night
// fleet-wide modal-relay storm. `user:` entries are exempt — user
// identities have no identity files (agents-table only, delivered via
// UI/bridges) and are local configuration by construction.
func (p *Permission) ResolveSupervisors(ctx context.Context, entries []string) ([]string, error) {
	return resolveSupervisorsFiltered(ctx, p.state, entries, p.isLocalAgent, "")
}

// ResolveSupervisorsExcluding is ResolveSupervisors with the modal owner
// excluded from the audience (thrum-x3fnh, the 09:48Z self-referential
// datapoint): the agent whose own modal is being relayed cannot read its
// inbox while modal-blocked, so addressing the relay to it is useless by
// construction — and self-referential supervisor traffic reads as a phantom
// nudge from the agent's own perspective.
func (p *Permission) ResolveSupervisorsExcluding(ctx context.Context, entries []string, excludeAgent string) ([]string, error) {
	return resolveSupervisorsFiltered(ctx, p.state, entries, p.isLocalAgent, excludeAgent)
}

// isLocalAgent reports whether the named agent is local to this daemon,
// backed by the injected localIdentityCheck (nudge.HasLocalIdentity, wired at
// daemon boot). A nil checker allows all agents (pre-wiring construction and
// unit tests that don't exercise locality).
func (p *Permission) isLocalAgent(name string) bool {
	if p.localIdentityCheck == nil {
		return true
	}
	return p.localIdentityCheck(name)
}

// resolveSupervisorsWithQuery is the legacy unfiltered resolution — kept as
// the seam the pre-x3fnh unit tests exercise. Production paths go through
// resolveSupervisorsFiltered.
func resolveSupervisorsWithQuery(ctx context.Context, s stateQuery, entries []string) ([]string, error) {
	return resolveSupervisorsFiltered(ctx, s, entries, nil, "")
}

// resolveSupervisorsFiltered resolves supervisor entries with two audience
// guards (thrum-x3fnh):
//
//   - isLocal (nil = allow all): agent recipients failing the check are
//     skipped on BOTH the named and role paths. user:-prefixed entries
//     bypass the check (see ResolveSupervisors doc).
//   - excludeAgent ("" = none): the named agent is dropped from the audience
//     on both paths (modal-owner self-exclusion).
func resolveSupervisorsFiltered(ctx context.Context, s stateQuery, entries []string, isLocal func(name string) bool, excludeAgent string) ([]string, error) {
	if len(entries) == 0 {
		entries = []string{"coordinator"}
	}
	seen := map[string]bool{}
	var out []string

	for _, entry := range entries {
		if name, ok := strings.CutPrefix(entry, "@"); ok {
			// Named agent or user. Liveness-check via IsAgentActive and
			// skip silently on dead/offline.
			if excludeAgent != "" && name == excludeAgent {
				continue
			}
			if isLocal != nil && !strings.HasPrefix(name, "user:") && !isLocal(name) {
				continue // remote agent — not actionable from this daemon (x3fnh)
			}
			active, err := s.IsAgentActive(ctx, name)
			if err != nil {
				return nil, err
			}
			if active && !seen[entry] {
				out = append(out, entry)
				seen[entry] = true
			}
			continue
		}

		// Role broadcast. ListActiveAgentsByRole already filters to agents
		// with a non-ended session, so we just need to @-prefix and dedup.
		// It has NO hostname/locality filter and returns synced REMOTE
		// agents too — the isLocal guard is what keeps supervisor fan-out
		// on this daemon's own panes (x3fnh).
		agents, err := s.ListActiveAgentsByRole(ctx, entry)
		if err != nil {
			return nil, err
		}
		for _, agentID := range agents {
			if excludeAgent != "" && agentID == excludeAgent {
				continue
			}
			if isLocal != nil && !isLocal(agentID) {
				continue
			}
			id := "@" + agentID
			if !seen[id] {
				out = append(out, id)
				seen[id] = true
			}
		}
	}
	return out, nil
}
