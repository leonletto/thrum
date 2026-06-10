package state

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// LocalDaemonIDs returns the set of daemon ids that belong to THIS host: the
// current daemonID plus every prior incarnation, derived from agents whose
// hostname matches this daemon's recorded hostname (daemon_identity.hostname).
//
// This closes the daemon-id-drift gap (thrum-tcqw): an agent registered under an
// OLD daemon id (e.g. before a daemon-id scheme change or a DB reset) still
// carries that stale id in origin_daemon, but is genuinely local. Keying local
// scope only on the *current* id wrongly treats such an agent as a peer — which
// is exactly why the original (thrum-agents v42) backfill skipped
// user:leon-letto (its origin_daemon was a prior-incarnation id) and left 234
// stuck-unread uncleared.
//
// LEAK-GUARD (thrum-edhn): the hostname match is STRICT (= the local hostname).
// It MUST NOT union blank-hostname rows — foreign peer daemon ids also appear on
// blank-hostname rows in the synced agents table (e.g. synced peer user rows),
// so including hostname='' would pull peer daemon ids into the local set and
// stamp peer read-state. Foreign daemon ids never appear with THIS host's
// hostname, so strict matching excludes them. (DB-validated on the source line:
// leon-letto's d_ees9pkfgax8p is anchored to hostname=leonsmacm1pro by 21
// sibling rows, while leontest/leondev/leonsmacmini ids only appear under their
// own hostnames.)
//
// Edge case (accepted): a host whose ONLY row under an old id was a blank-
// hostname user (no sibling agent-kind row to anchor it to this host) would not
// have that old id derived. In practice the local user co-exists with local
// agents under the same incarnation, so the id is anchored. The current daemonID
// is always included regardless.
func LocalDaemonIDs(ctx context.Context, db *safedb.DB, daemonID string) ([]string, error) {
	ids := map[string]struct{}{}
	if daemonID != "" {
		ids[daemonID] = struct{}{}
	}

	var hostname string
	// daemon_identity is the single-row current identity for this daemon.
	_ = db.QueryRowContext(ctx,
		`SELECT hostname FROM daemon_identity WHERE daemon_id = ?`, daemonID).Scan(&hostname)
	if hostname != "" {
		rows, err := db.QueryContext(ctx,
			`SELECT DISTINCT origin_daemon FROM agents WHERE hostname = ? AND origin_daemon != ''`, hostname)
		if err != nil {
			return nil, fmt.Errorf("local daemon ids: query by hostname: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, fmt.Errorf("local daemon ids: scan: %w", err)
			}
			ids[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("local daemon ids: iterate: %w", err)
		}
	}

	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out) // deterministic order (stable bind args)
	return out, nil
}

// LocalAgentScopeClause builds a SQL WHERE fragment (parenthesized) matching
// agents local to this daemon: origin_daemon in the local-id set, OR the legacy
// empty/NULL origin_daemon (pre-v22 rows and the column default). Returns the
// clause and the bind args (the local ids, in order). When localIDs is empty it
// still matches the legacy ''/NULL rows.
//
// Pair with LocalDaemonIDs: callers derive the set once, then splice this clause
// into their own query with the returned args at the matching placeholder slot.
func LocalAgentScopeClause(localIDs []string) (string, []any) {
	if len(localIDs) == 0 {
		return `(origin_daemon = '' OR origin_daemon IS NULL)`, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(localIDs)), ",")
	args := make([]any, 0, len(localIDs))
	for _, id := range localIDs {
		args = append(args, id)
	}
	return `(origin_daemon IN (` + ph + `) OR origin_daemon = '' OR origin_daemon IS NULL)`, args
}
