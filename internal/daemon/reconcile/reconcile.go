package reconcile

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/leonletto/thrum/internal/daemon"
)

// Manager is the xir.29 auto-reconcile decision engine. It owns the dial
// primitive, the local identity used in repair calls, and per-peer locks
// that serialize concurrent reconciles of the SAME peer while leaving
// reconciles of DIFFERENT peers free to run in parallel.
//
// Construct once at daemon startup; reuse from the boot-time ReconcileAll
// goroutine and from the send-time OnDialError hook via the PeerManager.
type Manager struct {
	registry *daemon.PeerRegistry
	dial     DialFunc
	local    DialerIdentity
	logger   *log.Logger

	// locks serializes concurrent reconciles for the SAME peer.
	// Different peers run in parallel. Keyed by peer name. Entries
	// are never deleted; the map footprint is bounded by the number
	// of peers paired with this daemon (small).
	//
	// Single Manager-wide mutex would serialize the ReconcileAll
	// worker pool into effective single-threaded execution (I8
	// review finding). Per-peer locking avoids that.
	locks sync.Map // map[string]*sync.Mutex
}

// Result captures a single reconcile attempt's outcome. Returned from
// ReconcileOne and collected by ReconcileAll for aggregated logging.
type Result struct {
	PeerName    string
	OK          bool
	Category    ErrCategory
	OldDaemonID string
	NewDaemonID string
	Err         error
}

// NewManager wires a reconcile manager to the given registry, dial func,
// and local identity. The local identity is sent in every peer.repair
// call so the listener can update its cached view of us in the same
// round-trip.
func NewManager(r *daemon.PeerRegistry, d DialFunc, local DialerIdentity) *Manager {
	return &Manager{
		registry: r,
		dial:     d,
		local:    local,
		logger:   log.Default(),
	}
}

// WithLogger overrides the default logger. Useful in tests for
// capturing reconcile diagnostics without polluting stderr.
func (m *Manager) WithLogger(l *log.Logger) *Manager {
	m.logger = l
	return m
}

// lockFor returns the per-peer mutex, allocating it on first access.
// Concurrent callers see the same mutex via sync.Map's LoadOrStore.
func (m *Manager) lockFor(peerName string) *sync.Mutex {
	v, _ := m.locks.LoadOrStore(peerName, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// ReconcileOne attempts to reconcile a single peer by name.
//
// Outcomes:
//   - dial succeeds + daemon_id unchanged: clear any prior drift flag,
//     return OK=true, no registry re-key.
//   - dial succeeds + daemon_id rotated: re-key the registry entry
//     atomically (Remove old key → Add new key under the new daemon_id,
//     matching repair.go:108-113 semantics), clear drift flag, return
//     OK=true.
//   - dial fails with CatUnreachable / CatTokenRejected: mark the entry
//     drift_reconcile_failed so `thrum peer list` can surface the cue
//     for manual --type repair. Return OK=false.
//   - dial fails with CatOther: log, do NOT flip status (transient),
//     return OK=false.
//
// The entire operation is atomic against concurrent reconciles of the
// SAME peer via the per-peer mutex; different peers reconcile in
// parallel.
func (m *Manager) ReconcileOne(ctx context.Context, peerName string) (Result, error) {
	peerMu := m.lockFor(peerName)
	peerMu.Lock()
	defer peerMu.Unlock()

	p := m.registry.FindPeerByName(peerName)
	if p == nil {
		return Result{PeerName: peerName}, fmt.Errorf("peer %q not found", peerName)
	}

	res := Result{
		PeerName:    peerName,
		OldDaemonID: p.DaemonID,
	}

	resp, err := m.dial(ctx, p.Address, p.Token, m.local)
	res.Category = CategorizeErr(err)
	res.Err = err

	if err != nil {
		if res.Category == CatUnreachable || res.Category == CatTokenRejected {
			if markErr := m.registry.SetReconcileStatus(p.DaemonID, StatusDriftReconcileFailed); markErr != nil {
				m.logger.Printf("reconcile: mark drift_reconcile_failed for %s: %v", peerName, markErr)
			}
		}
		return res, nil
	}

	res.NewDaemonID = resp.DaemonID
	res.OK = true

	// Re-key registry if daemon_id rotated. Remove old key before Add
	// under the new one; otherwise both would survive and drift further.
	// Pattern mirrors repair.go:108-113 (xir.27 listener-side).
	if res.OldDaemonID != resp.DaemonID && res.OldDaemonID != "" {
		if err := m.registry.RemovePeer(res.OldDaemonID); err != nil {
			m.logger.Printf("reconcile: remove stale entry %s: %v", res.OldDaemonID, err)
			return res, nil
		}
		refreshed := *p
		refreshed.DaemonID = resp.DaemonID
		refreshed.ReconcileStatus = StatusHealthy
		if resp.RepoName != "" {
			refreshed.RemoteRepoName = resp.RepoName
		}
		if resp.Hostname != "" {
			refreshed.RemoteHostname = resp.Hostname
		}
		if resp.RepoPath != "" {
			refreshed.RemoteRepoPath = resp.RepoPath
		}
		if resp.GitOriginURL != "" {
			refreshed.RemoteGitOriginURL = resp.GitOriginURL
		}
		if err := m.registry.AddPeer(&refreshed); err != nil {
			m.logger.Printf("reconcile: re-add entry %s: %v", resp.DaemonID, err)
			return res, nil
		}
		return res, nil
	}

	// Same daemon_id: the dial succeeded which proves liveness.
	// Clear any previously-set drift marker so a recovered peer
	// stops showing the manual-repair hint in `thrum peer list`.
	if p.ReconcileStatus == StatusDriftReconcileFailed {
		if err := m.registry.SetReconcileStatus(p.DaemonID, StatusHealthy); err != nil {
			m.logger.Printf("reconcile: clear drift status for %s: %v", peerName, err)
		}
	}
	return res, nil
}

// ReconcileOneHook is the daemon.ReconcileHook-compatible entry point.
// It runs the same ReconcileOne logic but returns a flattened tuple
// (ok, daemonIDChanged, category, err) to avoid forcing the daemon
// package to import reconcile.Result (which would cycle, since
// reconcile already imports daemon for PeerRegistry).
//
// The int category mirrors ErrCategory: 0=CatOK, 1=CatUnreachable,
// 2=CatTokenRejected, 3=CatOther. Kept in sync with dial.go's ErrCategory
// constants.
func (m *Manager) ReconcileOneHook(ctx context.Context, peerName string) (ok bool, daemonIDChanged bool, category int, err error) {
	res, e := m.ReconcileOne(ctx, peerName)
	if e != nil {
		return false, false, int(res.Category), e
	}
	return res.OK, res.OldDaemonID != res.NewDaemonID && res.OK, int(res.Category), nil
}

// ReconcileAll reconciles every peer in the registry, bounded to 4
// concurrent dials. Per-peer locking (see Manager.locks) means
// different peers proceed in parallel; same-peer is serialized.
// Errors from individual peers do NOT cancel the remaining peers —
// the reconcile pass proceeds through the full list.
func (m *Manager) ReconcileAll(ctx context.Context) []Result {
	peers := m.registry.ListPeers()
	results := make([]Result, 0, len(peers))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, p := range peers {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := m.ReconcileOne(ctx, p.Name)
			if err != nil {
				res.Err = err
			}
			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}
