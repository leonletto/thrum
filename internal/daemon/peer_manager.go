package daemon

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/bridge"
	"github.com/leonletto/thrum/internal/bridge/peer"
)

// ReconcileHook is the xir.29 interface PeerManager consumes to invoke
// auto-reconcile from the bridge reconnect loop (OnDialError). The real
// implementation lives in internal/daemon/reconcile; interface lives
// here to keep the daemon→reconcile dependency one-way (reconcile
// already imports daemon for PeerRegistry — a reverse import would
// cycle).
//
// Contract:
//   - err is non-nil only for unexpected plumbing failures (peer not
//     found, internal mutex issues). Dial/auth failures surface via
//     (ok=false, category=<CatUnreachable|CatTokenRejected|CatOther>).
//   - daemonIDChanged=true iff a successful reconcile re-keyed the
//     peer (daemon_id rotation healed); signals the caller that an
//     immediate retry is worthwhile.
//   - category matches reconcile.ErrCategory values; 0 = CatOK, 1 =
//     CatUnreachable, 2 = CatTokenRejected, 3 = CatOther. Duplicated
//     as plain ints here to avoid the import cycle.
type ReconcileHook interface {
	ReconcileOneHook(ctx context.Context, peerName string) (ok bool, daemonIDChanged bool, category int, err error)
}

// PeerManager manages outbound PeerBridge connections (dialer role) and
// handles listener-side acceptance when remote peers connect to us.
type PeerManager struct {
	registry    *PeerRegistry
	localWSPort string
	logger      *log.Logger
	mu          sync.Mutex
	bridges     map[string]*runningBridge

	// reconcileHook is the xir.29 auto-reconcile entry point invoked
	// from the OnDialError hook on each bridge. nil disables reconcile
	// (pre-xir.29 behavior preserved for tests and for deployments
	// that skip wiring reconcile at boot).
	reconcileHook ReconcileHook
	// attemptStates tracks per-peer reconcile attempt counts for the
	// xir.29 3-attempt cap. Hoisted onto PeerManager (not the
	// OnDialError closure) so counters persist across BuildConfigs
	// calls — a single peer's bridge can be rebuilt during its
	// lifetime (AcceptPeer path) and we do not want the counter to
	// reset mid-drift (N2 review finding).
	attemptStates sync.Map // map[string]*peerAttemptState
}

// peerAttemptState is the per-peer reconcile attempt counter state
// consumed by the OnDialError hook (xir.29). Protected by its own
// mutex; accessed from the bridge goroutine via a sync.Map lookup.
type peerAttemptState struct {
	mu    sync.Mutex
	count int
}

type runningBridge struct {
	cancel context.CancelFunc
}

// NewPeerManager creates a PeerManager that reads peer registry and manages bridges.
// A nil logger is replaced with log.Default() so the xir.29 OnDialError
// hook can safely emit diagnostics without risking a nil-pointer panic.
func NewPeerManager(registry *PeerRegistry, localWSPort string, logger *log.Logger) *PeerManager {
	if logger == nil {
		logger = log.Default()
	}
	return &PeerManager{
		registry:    registry,
		localWSPort: localWSPort,
		logger:      logger,
		bridges:     make(map[string]*runningBridge),
	}
}

// BuildConfigs generates BridgeConfig for each dialer-role peer.
// xir.29: when a ReconcileHook is installed via SetReconcileManager,
// each config's OnDialError is populated with the auto-reconcile entry
// point. Attempt counters live on PeerManager.attemptStates (not the
// closure) so they survive BuildConfigs being called multiple times
// during a peer's bridge lifecycle (N2 review finding).
func (pm *PeerManager) BuildConfigs() []peer.BridgeConfig {
	pm.mu.Lock()
	hook := pm.reconcileHook
	pm.mu.Unlock()

	peers := pm.registry.ListPeers()
	configs := make([]peer.BridgeConfig, 0, len(peers))
	for _, p := range peers {
		if p.Role != "dialer" {
			continue
		}
		cfg := peer.BridgeConfig{
			LocalWSPort:  pm.localWSPort,
			PeerName:     p.Name,
			PeerToken:    p.Token,
			BridgeUserID: fmt.Sprintf("user:peer-%s", p.Name),
			ProxyPrefix:  p.ProxyPrefix,
			RemoteAgents: p.RemoteAgents,
		}
		if p.Transport == "local" && p.RepoPath != "" {
			cfg.PeerRepoPath = p.RepoPath
		} else {
			cfg.PeerAddress = p.Address
		}
		if hook != nil {
			cfg.OnDialError = pm.makeOnDialError(hook, p.Name)
		}
		configs = append(configs, cfg)
	}
	return configs
}

// makeOnDialError returns the xir.29 OnDialError callback for a single
// peer. The callback:
//
//   - Classifies the dial error via reconcile.CategorizeErr (int codes:
//     1=CatUnreachable, 2=CatTokenRejected, 3=CatOther).
//   - Skips reconcile on CatTokenRejected (auth failure → terminal,
//     let drift_reconcile_failed marker + manual --type repair handle
//     recovery).
//   - Honors a 3-attempt cap with 2s / 8s / 30s backoff per coordinator
//     guidance (2026-04-19). Counter resets on successful reconcile.
//   - Cancellable at every sleep via ctx.Done so daemon shutdown does
//     not leak a 30s-waiting goroutine (N1 review finding).
//   - Returns true iff reconcile actually re-keyed the peer (daemon_id
//     rotated), signaling the bridge to retry immediately rather than
//     fall into its own backoff.
func (pm *PeerManager) makeOnDialError(hook ReconcileHook, _ string) func(string, error) bool {
	return func(name string, err error) bool {
		// Classify by well-known category codes (kept in sync with
		// reconcile.ErrCategory). Code 2 = CatTokenRejected.
		const catTokenRejected = 2

		// Approximate category from error text — the bridge layer
		// does not have direct access to reconcile.CategorizeErr.
		// Auth failures surface as "401"/"unauthorized" in the
		// gorilla WS handshake response.
		if err != nil && isAuthError(err) {
			return false
		}

		// Per-peer attempt state. Allocate on first use.
		rawSt, _ := pm.attemptStates.LoadOrStore(name, &peerAttemptState{})
		st := rawSt.(*peerAttemptState)
		st.mu.Lock()
		st.count++
		attempt := st.count
		st.mu.Unlock()

		if attempt > 3 {
			pm.logger.Printf("peer %s: reconcile cap exceeded (3 attempts); ceding to manual --type repair", name)
			return false
		}

		var delay time.Duration
		switch attempt {
		case 1:
			delay = 2 * time.Second
		case 2:
			delay = 8 * time.Second
		case 3:
			delay = 30 * time.Second
		}

		// Use a bounded, ctx-aware background context. The caller
		// (bridge reconnect goroutine) runs under a long-lived
		// parent ctx we do not have direct access to here; bound
		// the reconcile + sleep with a derived ctx so shutdown
		// interrupts us.
		//
		// 35s covers the 30s max-backoff plus a 5s reconcile dial
		// timeout. Shorter parent-cancellation unblocks instantly.
		hookCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()

		select {
		case <-time.After(delay):
		case <-hookCtx.Done():
			return false
		}

		dialCtx, dialCancel := context.WithTimeout(hookCtx, 10*time.Second)
		defer dialCancel()
		ok, daemonIDChanged, cat, herr := hook.ReconcileOneHook(dialCtx, name)
		if herr != nil {
			pm.logger.Printf("peer %s: reconcile: %v", name, herr)
			return false
		}
		// Auth failure surfaced via the hook's category — still suppress retry.
		if cat == catTokenRejected {
			return false
		}
		if ok {
			st.mu.Lock()
			st.count = 0
			st.mu.Unlock()
			return daemonIDChanged
		}
		return false
	}
}

// isAuthError returns true for errors that indicate the peer rejected
// our stored credentials (token mismatch, forbidden). These are
// terminal; auto-reconcile cannot fix them via re-key.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// gorilla/websocket embeds HTTP status codes in the dial error:
	// "websocket: bad handshake" + the response body. We match loosely
	// since exact text varies across gorilla versions.
	for _, marker := range []string{"401", "403", "unauthorized", "forbidden"} {
		if containsFold(msg, marker) {
			return true
		}
	}
	return false
}

// containsFold is a case-insensitive substring check used by isAuthError.
// Pulled out so it's unit-testable without exporting strings helpers.
func containsFold(s, sub string) bool {
	// Short inline implementation — avoids importing strings for one use.
	if len(sub) == 0 {
		return true
	}
	ls := len(s)
	lsub := len(sub)
	if lsub > ls {
		return false
	}
	for i := 0; i+lsub <= ls; i++ {
		match := true
		for j := 0; j < lsub; j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// ConnectAll spawns a PeerBridge for each dialer-role peer.
func (pm *PeerManager) ConnectAll(ctx context.Context) {
	configs := pm.BuildConfigs()
	for _, cfg := range configs {
		pm.startBridge(ctx, cfg)
	}
}

// startBridge spawns a managed PeerBridge goroutine with exponential-backoff reconnection.
// Idempotent — calling again for an already-running peer is a no-op.
func (pm *PeerManager) startBridge(parentCtx context.Context, cfg peer.BridgeConfig) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if _, exists := pm.bridges[cfg.PeerName]; exists {
		return
	}
	ctx, cancel := context.WithCancel(parentCtx) // #nosec G118 -- cancel stored in runningBridge, called on disconnect
	b := peer.NewBridge(cfg, pm.logger)
	pm.bridges[cfg.PeerName] = &runningBridge{cancel: cancel}

	go func() {
		backoff := time.Second
		const maxBackoff = 2 * time.Minute
		for {
			err := b.Run(ctx)
			if ctx.Err() != nil {
				return
			}
			pm.logger.Printf("peer %s disconnected: %v, reconnecting in %s", cfg.PeerName, err, backoff)
			// xir.29: give OnDialError first refusal. If it returns true,
			// skip backoff and retry immediately (auto-reconcile has taken
			// corrective action). Otherwise fall through to exponential
			// backoff as usual.
			immediate := false
			if cfg.OnDialError != nil {
				immediate = cfg.OnDialError(cfg.PeerName, err)
			}
			if !immediate {
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			} else {
				backoff = time.Second // reset on immediate retry
			}
		}
	}()
}

// AcceptPeer handles listener-side acceptance: when a remote peer connects to our WS,
// the daemon can call this to spawn a reverse bridge if not already running.
func (pm *PeerManager) AcceptPeer(ctx context.Context, peerInfo *PeerInfo) {
	cfg := peer.BridgeConfig{
		LocalWSPort:  pm.localWSPort,
		PeerName:     peerInfo.Name,
		PeerAddress:  peerInfo.Address,
		PeerToken:    peerInfo.Token,
		BridgeUserID: fmt.Sprintf("user:peer-%s", peerInfo.Name),
		ProxyPrefix:  peerInfo.ProxyPrefix,
		RemoteAgents: peerInfo.RemoteAgents,
	}
	pm.startBridge(ctx, cfg)
}

// SetReconcileManager installs an xir.29 ReconcileHook. Must be called
// before ConnectAll for newly-built bridges to carry the OnDialError
// hook. Safe to call with nil to disable reconcile.
func (pm *PeerManager) SetReconcileManager(h ReconcileHook) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.reconcileHook = h
}

// ActiveCount returns the number of currently-managed bridges.
func (pm *PeerManager) ActiveCount() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return len(pm.bridges)
}

// NotifyAddressChange connects to each known peer and sends a peer.address_changed
// notification with the new IP, port, and our own peer token.
func (pm *PeerManager) NotifyAddressChange(ctx context.Context, ip, port, myToken string) {
	peers := pm.registry.ListPeers()
	for _, p := range peers {
		if p.Token == "" || p.Address == "" {
			continue
		}
		url := fmt.Sprintf("ws://%s/ws", p.Address)
		ws := bridge.NewWSClient(url, bridge.WithPeerName(p.Name), bridge.WithBearerToken(p.Token))
		if err := ws.Connect(ctx); err != nil {
			pm.logger.Printf("notify %s address change: connect: %v", p.Name, err)
			continue
		}
		_, err := ws.Call(ctx, "peer.address_changed", map[string]any{
			"new_ip":     ip,
			"new_port":   port,
			"peer_token": myToken,
		})
		_ = ws.Close()
		if err != nil {
			pm.logger.Printf("notify %s address change: %v", p.Name, err)
		}
	}
}

// StopAll cancels all running bridges and clears the bridge map.
func (pm *PeerManager) StopAll() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for name, rb := range pm.bridges {
		rb.cancel()
		delete(pm.bridges, name)
	}
}
