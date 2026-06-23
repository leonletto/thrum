package state

import (
	"context"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// TestLocalDaemonIDs_HostnameDerivedSet_ExcludesPeers is the leak-guard-critical
// test (thrum-b6qw / thrum-edhn): the local daemon-id set is the current id plus
// every prior incarnation of THIS host (derived from agents.hostname = the local
// hostname), and it must EXCLUDE foreign peer daemon ids — including the trap
// where a foreign id also appears on blank-hostname (synced user) rows.
func TestLocalDaemonIDs_HostnameDerivedSet_ExcludesPeers(t *testing.T) {
	db := newStateTestDB(t)
	const (
		current = "d_current"
		stale   = "d_stale_old_incarnation"
		peer    = "d_peer_foreign"
		hostA   = "hostA"
		hostB   = "hostB"
	)
	// This daemon's identity (single-row): hostname=hostA.
	mustExec(t, db, `INSERT INTO daemon_identity(daemon_id, repo_name, hostname, repo_path, init_at, updated_at) VALUES(?, 'thrum', ?, '/x', 't', 't')`, current, hostA)

	// LOCAL agents on hostA: one under the current id, one under a stale prior id.
	mustExec(t, db, `INSERT INTO agents(agent_id, kind, role, module, hostname, registered_at, origin_daemon) VALUES('impl_now','claude','implementer','m', ?, 't', ?)`, hostA, current)
	mustExec(t, db, `INSERT INTO agents(agent_id, kind, role, module, hostname, registered_at, origin_daemon) VALUES('impl_old','claude','implementer','m', ?, 't', ?)`, hostA, stale)
	// The stuck local web user: blank hostname, stale origin id (the bug repro).
	mustExec(t, db, `INSERT INTO agents(agent_id, kind, role, module, hostname, registered_at, origin_daemon) VALUES('user:leon-letto','user','','ui', '', 't', ?)`, stale)
	// FOREIGN peer agent on hostB.
	mustExec(t, db, `INSERT INTO agents(agent_id, kind, role, module, hostname, registered_at, origin_daemon) VALUES('impl_peer','claude','implementer','m', ?, 't', ?)`, hostB, peer)
	// LEAK TRAP: a synced peer USER row carries the foreign id with BLANK hostname.
	mustExec(t, db, `INSERT INTO agents(agent_id, kind, role, module, hostname, registered_at, origin_daemon) VALUES('user:peer-x','user','','ui', '', 't', ?)`, peer)

	ids, err := LocalDaemonIDs(context.Background(), safedb.New(db), current)
	if err != nil {
		t.Fatalf("LocalDaemonIDs: %v", err)
	}
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	if !set[current] {
		t.Errorf("local set must include current daemon id %q; got %v", current, ids)
	}
	if !set[stale] {
		t.Errorf("local set must include stale prior-incarnation id %q (leon-letto's bug); got %v", stale, ids)
	}
	if set[peer] {
		t.Fatalf("LEAK: local set wrongly includes foreign peer id %q; got %v", peer, ids)
	}
}

func TestLocalAgentScopeClause(t *testing.T) {
	clause, args := LocalAgentScopeClause([]string{"a", "b"})
	want := `(origin_daemon IN (?,?) OR origin_daemon = '' OR origin_daemon IS NULL)`
	if clause != want {
		t.Errorf("clause = %q, want %q", clause, want)
	}
	if len(args) != 2 || args[0] != "a" || args[1] != "b" {
		t.Errorf("args = %v, want [a b]", args)
	}

	clause0, args0 := LocalAgentScopeClause(nil)
	want0 := `(origin_daemon = '' OR origin_daemon IS NULL)`
	if clause0 != want0 {
		t.Errorf("empty clause = %q, want %q", clause0, want0)
	}
	if args0 != nil {
		t.Errorf("empty args = %v, want nil", args0)
	}
}
