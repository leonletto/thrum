package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/bridge"
)

// PeerTransport connects to a remote Thrum daemon's WebSocket.
// Implements bridge.TransportBridge.
type PeerTransport struct {
	name     string
	address  string // host:port (empty for local peers using repo_path)
	token    string
	repoPath string // For local peers: read port from .thrum/var/ws.port
	client   *bridge.WSClient
}

// Compile-time interface check.
var _ bridge.TransportBridge = (*PeerTransport)(nil)

func NewPeerTransport(name, address, token string) *PeerTransport {
	return &PeerTransport{name: name, address: address, token: token}
}

func NewLocalPeerTransport(name, repoPath, token string) *PeerTransport {
	return &PeerTransport{name: name, repoPath: repoPath, token: token}
}

func (t *PeerTransport) PeerName() string { return t.name }

func (t *PeerTransport) Connect(ctx context.Context) error {
	addr, err := t.resolveAddress()
	if err != nil {
		return fmt.Errorf("resolve address for %s: %w", t.name, err)
	}

	url := fmt.Sprintf("ws://%s/ws", addr)
	opts := []bridge.DialOption{bridge.WithPeerName(t.name)}
	if t.token != "" {
		opts = append(opts, bridge.WithBearerToken(t.token))
	}
	t.client = bridge.NewWSClient(url, opts...)
	return t.client.Connect(ctx)
}

func (t *PeerTransport) resolveAddress() (string, error) {
	if t.repoPath != "" {
		portFile := filepath.Join(t.repoPath, ".thrum", "var", "ws.port")
		data, err := os.ReadFile(portFile) // #nosec G304 -- portFile derived from trusted config, not user input
		if err != nil {
			return "", fmt.Errorf("read port file %s: %w", portFile, err)
		}
		port := strings.TrimSpace(string(data))
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	return t.address, nil
}

func (t *PeerTransport) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if t.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	return t.client.Call(ctx, method, params)
}

func (t *PeerTransport) Notifications() <-chan bridge.Notification {
	if t.client == nil {
		ch := make(chan bridge.Notification)
		close(ch)
		return ch
	}
	return t.client.Notifications()
}

func (t *PeerTransport) Close() error {
	if t.client != nil {
		return t.client.Close()
	}
	return nil
}

func (t *PeerTransport) Connected() bool {
	return t.client != nil && t.client.Connected()
}
