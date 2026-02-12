package daemon

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
)

// DefaultDiscoveryInterval is the default interval for periodic peer discovery.
const DefaultDiscoveryInterval = 5 * time.Minute

// PeerDiscoverer discovers thrum daemons on the tailnet and registers them in the peer registry.
type PeerDiscoverer struct {
	tsClient    TailscaleStatusProvider
	peers       *PeerRegistry
	syncClient  *SyncClient
	port        int    // port to connect to peer daemons
	requiredTag string // tag to filter peers by (e.g., "tag:thrum-daemon")
}

// TailscaleStatusProvider abstracts the Tailscale LocalClient.Status() call for testability.
type TailscaleStatusProvider interface {
	Status(ctx context.Context) (*ipnstate.Status, error)
}

// localClientAdapter adapts *local.Client to TailscaleStatusProvider.
type localClientAdapter struct {
	client *local.Client
}

func (a *localClientAdapter) Status(ctx context.Context) (*ipnstate.Status, error) {
	return a.client.Status(ctx)
}

// NewPeerDiscoverer creates a new peer discoverer.
func NewPeerDiscoverer(tsClient *local.Client, peers *PeerRegistry, syncClient *SyncClient, port int) *PeerDiscoverer {
	return &PeerDiscoverer{
		tsClient:    &localClientAdapter{client: tsClient},
		peers:       peers,
		syncClient:  syncClient,
		port:        port,
		requiredTag: "tag:thrum-daemon",
	}
}

// NewPeerDiscovererWithProvider creates a peer discoverer with an injectable status provider (for testing).
func NewPeerDiscovererWithProvider(provider TailscaleStatusProvider, peers *PeerRegistry, syncClient *SyncClient, port int) *PeerDiscoverer {
	return &PeerDiscoverer{
		tsClient:    provider,
		peers:       peers,
		syncClient:  syncClient,
		port:        port,
		requiredTag: "tag:thrum-daemon",
	}
}

// DiscoverPeers queries the Tailscale network for peer daemons and registers them.
// Returns the number of peers discovered and any error.
func (d *PeerDiscoverer) DiscoverPeers(ctx context.Context) (int, error) {
	status, err := d.tsClient.Status(ctx)
	if err != nil {
		return 0, fmt.Errorf("get tailscale status: %w", err)
	}

	discovered := 0
	for _, peer := range status.Peer {
		if !peer.Online {
			continue
		}

		if !d.hasRequiredTag(peer) {
			continue
		}

		// Build peer address from DNS name or hostname
		host := strings.TrimSuffix(peer.DNSName, ".")
		if host == "" {
			host = peer.HostName
		}
		if host == "" {
			continue
		}
		addr := fmt.Sprintf("%s:%d", host, d.port)

		// Query peer info via RPC
		info, err := d.syncClient.QueryPeerInfo(addr)
		if err != nil {
			log.Printf("peer_discovery: failed to query %s at %s: %v", peer.HostName, addr, err)
			// Register with hostname-derived info so we know it exists
			_ = d.peers.AddPeer(&PeerInfo{
				DaemonID: "d_" + peer.HostName,
				Hostname: peer.HostName,
				FQDN:     strings.TrimSuffix(peer.DNSName, "."),
				Port:     d.port,
				Status:   "offline",
			})
			discovered++
			continue
		}

		_ = d.peers.AddPeer(&PeerInfo{
			DaemonID:  info.DaemonID,
			Hostname:  peer.HostName,
			FQDN:      strings.TrimSuffix(peer.DNSName, "."),
			Port:      d.port,
			PublicKey: info.PublicKey,
			Status:    "active",
		})
		discovered++
	}

	return discovered, nil
}

// StartPeriodicDiscovery runs peer discovery at the given interval until the context is cancelled.
func (d *PeerDiscoverer) StartPeriodicDiscovery(ctx context.Context, interval time.Duration) {
	// Run initial discovery immediately
	n, err := d.DiscoverPeers(ctx)
	if err != nil {
		log.Printf("peer_discovery: initial discovery failed: %v", err)
	} else {
		log.Printf("peer_discovery: initial discovery found %d peers", n)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := d.DiscoverPeers(ctx)
			if err != nil {
				log.Printf("peer_discovery: periodic discovery failed: %v", err)
			} else if n > 0 {
				log.Printf("peer_discovery: periodic discovery found %d peers", n)
			}
		}
	}
}

// hasRequiredTag checks whether a peer has the required tag.
func (d *PeerDiscoverer) hasRequiredTag(peer *ipnstate.PeerStatus) bool {
	if peer.Tags == nil {
		return false
	}
	for i := range peer.Tags.Len() {
		if peer.Tags.At(i) == d.requiredTag {
			return true
		}
	}
	return false
}
