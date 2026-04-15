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
func (p *Permission) ResolveSupervisors(ctx context.Context, entries []string) ([]string, error) {
	return resolveSupervisorsWithQuery(ctx, p.state, entries)
}

func resolveSupervisorsWithQuery(ctx context.Context, s stateQuery, entries []string) ([]string, error) {
	if len(entries) == 0 {
		entries = []string{"coordinator"}
	}
	seen := map[string]bool{}
	var out []string

	for _, entry := range entries {
		if name, ok := strings.CutPrefix(entry, "@"); ok {
			// Named agent or user. Liveness-check via IsAgentActive and
			// skip silently on dead/offline.
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

		// Role broadcast. ListActiveAgentsByRole already filters to
		// agents with a non-ended session, so we just need to @-prefix
		// and dedup.
		agents, err := s.ListActiveAgentsByRole(ctx, entry)
		if err != nil {
			return nil, err
		}
		for _, agentID := range agents {
			id := "@" + agentID
			if !seen[id] {
				out = append(out, id)
				seen[id] = true
			}
		}
	}
	return out, nil
}
