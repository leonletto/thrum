package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
	"tailscale.com/types/views"
)

// makeNodeKey generates a unique NodePublic key for testing.
func makeNodeKey() key.NodePublic {
	return key.NewNode().Public()
}

// mockStatusProvider implements TailscaleStatusProvider for testing.
type mockStatusProvider struct {
	status *ipnstate.Status
	err    error
}

func (m *mockStatusProvider) Status(_ context.Context) (*ipnstate.Status, error) {
	return m.status, m.err
}

// makePeerWithTags creates a PeerStatus with the given tags for testing.
func makePeerWithTags(hostname, dnsName string, online bool, tags []string) *ipnstate.PeerStatus {
	ps := &ipnstate.PeerStatus{
		HostName: hostname,
		DNSName:  dnsName,
		Online:   online,
	}
	if len(tags) > 0 {
		s := views.SliceOf(tags)
		ps.Tags = &s
	}
	return ps
}

func TestPeerDiscoverer_DiscoverPeers(t *testing.T) {
	// Set up a mock RPC server that responds to sync.peer_info
	reg := NewSyncRegistry()
	_ = reg.Register("sync.peer_info", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{
			"daemon_id":  "d_test-peer",
			"hostname":   "test-peer",
			"public_key": "",
		}, nil
	})
	_ = reg.Register("sync.pull", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"events": []any{}, "next_sequence": 0, "more_available": false}, nil
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go reg.ServeSyncRPC(ctx, conn, "test-peer")
		}
	}()

	// Get the port our mock server is listening on

	// Create a mock status provider with one tagged peer
	provider := &mockStatusProvider{
		status: &ipnstate.Status{
			Peer: map[key.NodePublic]*ipnstate.PeerStatus{
				{}: makePeerWithTags("test-peer", "test-peer.tailnet.ts.net.", true, []string{"tag:thrum-daemon"}),
			},
		},
	}

	// Create peer registry and discoverer
	peerReg, err := NewPeerRegistry(t.TempDir() + "/peers.json")
	if err != nil {
		t.Fatalf("peer registry: %v", err)
	}

	// Use 127.0.0.1 directly since DNS won't resolve test hostnames
	// Override the discoverer's port to match our mock server
	syncClient := NewSyncClient()

	discoverer := NewPeerDiscovererWithProvider(provider, peerReg, syncClient, 0)
	// We need to test using the mock server address, so let's create a custom test
	// that calls the mock server directly

	// First test: verify discovery finds tagged peers
	// Override the DNS name to point to our mock server
	provider.status.Peer = map[key.NodePublic]*ipnstate.PeerStatus{
		{}: makePeerWithTags("test-peer", "127.0.0.1", true, []string{"tag:thrum-daemon"}),
	}

	// Update port to mock server port
	port := ln.Addr().(*net.TCPAddr).Port
	discoverer.port = port

	n, err := discoverer.DiscoverPeers(ctx)
	if err != nil {
		t.Fatalf("DiscoverPeers: %v", err)
	}

	if n != 1 {
		t.Errorf("discovered %d peers, want 1", n)
	}

	// Check peer was registered
	peers := peerReg.ListPeers()
	if len(peers) != 1 {
		t.Fatalf("peer registry has %d peers, want 1", len(peers))
	}
	if peers[0].DaemonID != "d_test-peer" {
		t.Errorf("peer daemon_id = %q, want d_test-peer", peers[0].DaemonID)
	}
	if peers[0].Status != "active" {
		t.Errorf("peer status = %q, want active", peers[0].Status)
	}
}

func TestPeerDiscoverer_FiltersByTag(t *testing.T) {
	provider := &mockStatusProvider{
		status: &ipnstate.Status{
			Peer: map[key.NodePublic]*ipnstate.PeerStatus{
				// Peer with thrum-daemon tag
				makeNodeKey(): makePeerWithTags("tagged-peer", "tagged.tailnet.ts.net.", true, []string{"tag:thrum-daemon"}),
				// Peer without tag
				makeNodeKey(): makePeerWithTags("other-peer", "other.tailnet.ts.net.", true, []string{"tag:other"}),
				// Peer with no tags
				makeNodeKey(): makePeerWithTags("no-tag-peer", "notag.tailnet.ts.net.", true, nil),
				// Offline tagged peer
				makeNodeKey(): makePeerWithTags("offline-peer", "offline.tailnet.ts.net.", false, []string{"tag:thrum-daemon"}),
			},
		},
	}

	peerReg, err := NewPeerRegistry(t.TempDir() + "/peers.json")
	if err != nil {
		t.Fatalf("peer registry: %v", err)
	}

	discoverer := NewPeerDiscovererWithProvider(provider, peerReg, NewSyncClient(), 9100)

	n, err := discoverer.DiscoverPeers(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPeers: %v", err)
	}

	// Should only discover the 1 online tagged peer (even though peer_info will fail)
	if n != 1 {
		t.Errorf("discovered %d peers, want 1 (only online tagged)", n)
	}

	peers := peerReg.ListPeers()
	if len(peers) != 1 {
		t.Fatalf("peer registry has %d peers, want 1", len(peers))
	}
	// Peer will be offline because peer_info failed
	if peers[0].Hostname != "tagged-peer" {
		t.Errorf("discovered peer hostname = %q, want tagged-peer", peers[0].Hostname)
	}
}

func TestPeerDiscoverer_HandlesEmptyPeerList(t *testing.T) {
	provider := &mockStatusProvider{
		status: &ipnstate.Status{
			Peer: map[key.NodePublic]*ipnstate.PeerStatus{},
		},
	}

	peerReg, err := NewPeerRegistry(t.TempDir() + "/peers.json")
	if err != nil {
		t.Fatalf("peer registry: %v", err)
	}

	discoverer := NewPeerDiscovererWithProvider(provider, peerReg, NewSyncClient(), 9100)

	n, err := discoverer.DiscoverPeers(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPeers: %v", err)
	}
	if n != 0 {
		t.Errorf("discovered %d peers, want 0", n)
	}
}

func TestPeerDiscoverer_PeriodicDiscovery(t *testing.T) {
	provider := &mockStatusProvider{
		status: &ipnstate.Status{
			Peer: map[key.NodePublic]*ipnstate.PeerStatus{},
		},
	}

	peerReg, err := NewPeerRegistry(t.TempDir() + "/peers.json")
	if err != nil {
		t.Fatalf("peer registry: %v", err)
	}

	discoverer := NewPeerDiscovererWithProvider(provider, peerReg, NewSyncClient(), 9100)

	ctx, cancel := context.WithCancel(context.Background())

	// Start periodic discovery with very short interval
	done := make(chan struct{})
	go func() {
		discoverer.StartPeriodicDiscovery(ctx, 50*time.Millisecond)
		close(done)
	}()

	// Let it run for a bit
	time.Sleep(150 * time.Millisecond)

	// Cancel and wait for clean shutdown
	cancel()
	select {
	case <-done:
		// Clean shutdown
	case <-time.After(time.Second):
		t.Error("periodic discovery didn't stop after context cancel")
	}
}
