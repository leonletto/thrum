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

// PeerManager manages outbound PeerBridge connections (dialer role) and
// handles listener-side acceptance when remote peers connect to us.
type PeerManager struct {
	registry    *PeerRegistry
	localWSPort string
	logger      *log.Logger
	mu          sync.Mutex
	bridges     map[string]*runningBridge
}

type runningBridge struct {
	cancel context.CancelFunc
}

// NewPeerManager creates a PeerManager that reads peer registry and manages bridges.
func NewPeerManager(registry *PeerRegistry, localWSPort string, logger *log.Logger) *PeerManager {
	return &PeerManager{
		registry:    registry,
		localWSPort: localWSPort,
		logger:      logger,
		bridges:     make(map[string]*runningBridge),
	}
}

// BuildConfigs generates BridgeConfig for each dialer-role peer.
func (pm *PeerManager) BuildConfigs() []peer.BridgeConfig {
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
		configs = append(configs, cfg)
	}
	return configs
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
	ctx, cancel := context.WithCancel(parentCtx)
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
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
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
		url := fmt.Sprintf("ws://%s/ws?token=%s", p.Address, p.Token)
		ws := bridge.NewWSClient(url, bridge.WithPeerName(p.Name))
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
