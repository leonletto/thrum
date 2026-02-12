package daemon

import (
	"fmt"
	"net"
	"os"

	"github.com/leonletto/thrum/internal/config"
	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

// TsnetListener wraps a tsnet server and its listener for sync connections.
type TsnetListener struct {
	server   *tsnet.Server
	listener net.Listener
}

// NewTsnetServer creates a tsnet server and listener from the given config.
// The caller is responsible for calling Close() when done.
func NewTsnetServer(cfg config.TailscaleConfig) (*TsnetListener, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("tailscale sync is not enabled")
	}

	// Ensure state directory exists
	if cfg.StateDir != "" {
		if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
			return nil, fmt.Errorf("create tsnet state directory %s: %w", cfg.StateDir, err)
		}
	}

	// Get auth key
	authKey := cfg.AuthKey
	if authKey == "" {
		authKey = os.Getenv("THRUM_TS_AUTHKEY")
	}
	if authKey == "" {
		return nil, fmt.Errorf("tailscale auth key not set (THRUM_TS_AUTHKEY)")
	}

	srv := &tsnet.Server{
		Hostname: cfg.Hostname,
		AuthKey:  authKey,
		Dir:      cfg.StateDir,
	}

	// Set ControlURL for Headscale / self-hosted deployments
	if cfg.ControlURL != "" {
		srv.ControlURL = cfg.ControlURL
	}

	// Start listener
	ln, err := srv.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("tsnet listen on :%d: %w", cfg.Port, err)
	}

	return &TsnetListener{
		server:   srv,
		listener: ln,
	}, nil
}

// Accept waits for and returns the next connection.
func (t *TsnetListener) Accept() (net.Conn, error) {
	return t.listener.Accept()
}

// Addr returns the listener's network address.
func (t *TsnetListener) Addr() net.Addr {
	return t.listener.Addr()
}

// LocalClient returns the Tailscale LocalClient for this tsnet server.
// Used for peer discovery and WhoIs lookups.
func (t *TsnetListener) LocalClient() (*local.Client, error) {
	return t.server.LocalClient()
}

// Close stops the tsnet server and listener.
func (t *TsnetListener) Close() error {
	lnErr := t.listener.Close()
	srvErr := t.server.Close()
	if lnErr != nil {
		return fmt.Errorf("close listener: %w", lnErr)
	}
	if srvErr != nil {
		return fmt.Errorf("close server: %w", srvErr)
	}
	return nil
}
