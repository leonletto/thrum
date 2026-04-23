package daemon

import (
	"context"
	"fmt"
	"log"
	"strings"
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

	// reconcileDelayFn overrides the 2s/8s/30s backoff schedule. Used
	// by tests to drive the real reset-on-success path without a 2s
	// sleep (I5 review finding). Production leaves this nil.
	reconcileDelayFn func(attempt int) time.Duration
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
//
// localWSPort MAY be "" at construction when the daemon hasn't bound its ws
// listener yet (thrum-1f4y: early construction lets handlers capture the
// manager before wsPort resolves). Use SetLocalWSPort once the port binds;
// bridges can only start after it is populated.
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

// SetLocalWSPort updates the daemon's WS port. Must be called once during
// daemon boot after the WS listener binds, BEFORE any goroutine can read
// pm.localWSPort (i.e. before ConnectAll / AcceptPeer / ConnectPeer ever
// fire). The happens-before established by goroutine creation (or by the
// handler-registration ordering of the RPC server) makes the subsequent
// lock-free reads safe.
func (pm *PeerManager) SetLocalWSPort(port string) {
	pm.localWSPort = port
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
		cfg := pm.buildConfigForPeer(p)
		if hook != nil {
			cfg.OnDialError = pm.makeOnDialError(hook, p.Name)
		}
		configs = append(configs, cfg)
	}
	return configs
}

// buildConfigForPeer produces the BridgeConfig for a single peer. Shared
// by BuildConfigs, ConnectPeer, and AcceptPeer so the address/repo-path
// selection stays in one place. xir.29's OnDialError hook is attached
// by BuildConfigs (dialer-only) and is intentionally NOT set here;
// AcceptPeer and the ConnectPeer path into peer.join do not need the
// reconcile callback (listener-side reverse bridges and newly-paired
// dialers both rely on the first dial succeeding via stored secrets).
func (pm *PeerManager) buildConfigForPeer(p *PeerInfo) peer.BridgeConfig {
	// thrum-bew3: sanitize p.Name before folding into BridgeUserID. The
	// peer name is typically a hostname (dots) or similar free-form
	// identifier, but the daemon's user.register validator rejects
	// anything outside [a-zA-Z0-9_-] and caps length at 32. Without this,
	// the bridge handshake fails on user.register and reconnect-loops
	// forever.
	//
	// Non-ASCII peer names (e.g. "北京") sanitize to empty and would
	// reproduce the same "user:peer-" (empty-suffix) rejection path.
	// Fall back to the peer's DaemonID ULID body (d_ prefix stripped —
	// redundant in a peer-derived context). The resulting username is
	// "peer-<suffix>" which must fit inside the 32-char usernameRegex
	// cap; 27 chars of suffix leaves 5 for the "peer-" prefix.
	suffix := SanitizeProxyPrefix(p.Name)
	if suffix == "" {
		suffix = strings.TrimPrefix(p.DaemonID, "d_")
	}
	if len(suffix) > 27 {
		suffix = suffix[:27]
	}
	cfg := peer.BridgeConfig{
		LocalWSPort:  pm.localWSPort,
		PeerName:     p.Name,
		PeerToken:    p.Token,
		BridgeUserID: "user:peer-" + suffix,
		ProxyPrefix:  p.ProxyPrefix,
		RemoteAgents: p.RemoteAgents,
	}
	if p.Transport == "local" && p.RepoPath != "" {
		cfg.PeerRepoPath = p.RepoPath
	} else {
		cfg.PeerAddress = p.Address
	}
	return cfg
}

// makeOnDialError returns the xir.29 OnDialError callback for a single
// peer. The callback:
//
//   - Honors a 3-attempt cap with 2s / 8s / 30s backoff per coordinator
//     guidance (2026-04-19). Counter resets on successful reconcile.
//   - Calls ReconcileOneHook; the hook returns a category code that
//     classifies the failure. CatTokenRejected (code 2) = auth failure
//     → terminal, do not retry; reconcile has already flagged the
//     peer drift_reconcile_failed on the path to returning.
//   - Cancellable at every sleep via the bridge-supplied ctx.Done so
//     daemon shutdown does not leak a 30s-waiting goroutine (B2 + N1
//     review findings). ctx comes from the bridge reconnect loop,
//     which cancels on StopAll / ctx.Done.
//   - Returns true iff reconcile actually re-keyed the peer (daemon_id
//     rotated), signaling the bridge to retry immediately rather than
//     fall into its own backoff.
//
// Pre-gates on error string markers (401/403/unauthorized/forbidden)
// were dropped in favor of the single-source-of-truth category gate
// (I3 review finding) — one extra one-shot WSDial is a small cost for
// eliminating the gap where a wrapped ErrTokenRejected from peer.repair
// (no "401" in its error text) slipped past the pre-gate.
func (pm *PeerManager) makeOnDialError(hook ReconcileHook, _ string) func(context.Context, string, error) bool {
	return func(ctx context.Context, name string, err error) bool {
		// Category codes kept in sync with reconcile.ErrCategory:
		// 1=CatUnreachable, 2=CatTokenRejected, 3=CatOther.
		const catTokenRejected = 2

		// Per-peer attempt state. Allocate on first use.
		rawSt, _ := pm.attemptStates.LoadOrStore(name, &peerAttemptState{})
		st, ok := rawSt.(*peerAttemptState)
		if !ok {
			pm.logger.Printf("peer %s: attemptStates type assertion failed; skipping reconcile", name)
			return false
		}
		st.mu.Lock()
		st.count++
		attempt := st.count
		st.mu.Unlock()

		if attempt > 3 {
			pm.logger.Printf("peer %s: reconcile cap exceeded (3 attempts); ceding to manual --type repair", name)
			return false
		}

		delay := pm.reconcileDelayForAttempt(attempt)
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return false
			}
		}

		// Reconcile dial bounded to 10s; inherits parent ctx cancel.
		dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
		defer dialCancel()
		ok, daemonIDChanged, cat, herr := hook.ReconcileOneHook(dialCtx, name)
		if herr != nil {
			pm.logger.Printf("peer %s: reconcile: %v", name, herr)
			return false
		}
		// Auth failure (I3 unified gate): terminal, do not retry.
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

// reconcileDelayForAttempt returns the 2s/8s/30s backoff schedule for
// the xir.29 OnDialError retry cap. Extracted as a method so tests can
// override via pm.reconcileDelayFn (test-only, unexported field) to
// keep unit-test runtime low without sacrificing the production-path
// exercise (I5 review finding).
func (pm *PeerManager) reconcileDelayForAttempt(attempt int) time.Duration {
	if pm.reconcileDelayFn != nil {
		return pm.reconcileDelayFn(attempt)
	}
	switch attempt {
	case 1:
		return 2 * time.Second
	case 2:
		return 8 * time.Second
	case 3:
		return 30 * time.Second
	default:
		return 0
	}
}

// ConnectAll spawns a PeerBridge for each dialer-role peer.
func (pm *PeerManager) ConnectAll(ctx context.Context) {
	configs := pm.BuildConfigs()
	for _, cfg := range configs {
		pm.startBridge(ctx, cfg)
	}
}

// ConnectPeer spawns a PeerBridge for a single peer immediately. Used by
// peer.join (and future peer.add dialer paths) so newly-paired peers do
// not wait for the next daemon restart to come online (thrum-1f4y).
// Idempotent — the underlying startBridge is a no-op if a bridge for
// this peer name is already running. The OnDialError reconcile hook is
// intentionally NOT attached here: a freshly paired peer's first dial
// should succeed with the stored secrets; any drift that surfaces
// afterward lands on ConnectAll's normal path at the next daemon boot.
func (pm *PeerManager) ConnectPeer(ctx context.Context, p *PeerInfo) {
	if p == nil || p.Role != "dialer" {
		return
	}
	pm.startBridge(ctx, pm.buildConfigForPeer(p))
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
			// backoff as usual. Pass ctx so shutdown cancels any reconcile
			// + backoff sleep inside the hook (B2 review finding).
			immediate := false
			if cfg.OnDialError != nil {
				immediate = cfg.OnDialError(ctx, cfg.PeerName, err)
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
	if peerInfo == nil {
		return
	}
	pm.startBridge(ctx, pm.buildConfigForPeer(peerInfo))
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
