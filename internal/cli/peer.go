package cli

import (
	"fmt"
	"strings"
	"time"
)

// --- Request/Response types (mirror rpc package) ---

// PeerStartPairingResult is the result of starting a pairing session.
type PeerStartPairingResult struct {
	Code    string `json:"code"`
	Address string `json:"address,omitempty"` // local peer address (ip:port) — class depends on Type
	// Transport echoes the daemon's chosen transport label. From xir.27
	// onwards: tailscale | local | network | a-sync. Used by the CLI to
	// surface "Pairing code (transport=X): ..." consistently.
	Transport string `json:"transport,omitempty"`
}

// PeerWaitPairingResult is the result of waiting for pairing completion.
type PeerWaitPairingResult struct {
	Status       string `json:"status"` // "paired" or "timeout"
	PeerName     string `json:"peer_name,omitempty"`
	PeerAddress  string `json:"peer_address,omitempty"`
	PeerDaemonID string `json:"peer_daemon_id,omitempty"`
	Message      string `json:"message,omitempty"`
}

// PeerJoinResult is the result of joining a remote peer.
type PeerJoinResult struct {
	Status       string `json:"status"` // "paired" or "error"
	PeerName     string `json:"peer_name,omitempty"`
	PeerDaemonID string `json:"peer_daemon_id,omitempty"`
	Message      string `json:"message,omitempty"`
}

// PeerListEntry is a single peer in the list.
type PeerListEntry struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	LastSync string `json:"last_sync"`
	LastSeq  int64  `json:"last_synced_seq"`
}

// PeerDetailedStatusEntry is the detailed status of a single peer.
type PeerDetailedStatusEntry struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	HasToken bool   `json:"has_token"`
	PairedAt string `json:"paired_at"`
	LastSync string `json:"last_sync"`
	LastSeq  int64  `json:"last_synced_seq"`
}

// --- RPC client functions ---

// PeerStartPairingParams are optional parameters for starting pairing.
type PeerStartPairingParams struct {
	AuthKey string `json:"auth_key,omitempty"`
	// Type is the user-selected transport (tailscale|local|network|a-sync).
	// Required from xir.27 onwards. The daemon dispatches peercode emission
	// and listener choice based on this value. "repair" is rejected here
	// (peer add cannot reconcile an existing peer); use PeerJoinParams.Type
	// for repair flow.
	Type string `json:"type,omitempty"`
	// Address is the user-supplied LAN IP for --type network. Empty for
	// other types.
	Address string `json:"address,omitempty"`
	// Remote is the user-supplied git URL for --type a-sync. Empty for
	// other types.
	Remote string `json:"remote,omitempty"`
}

// PeerJoinParams holds the per-call parameters for `thrum peer join`.
// Required from xir.27 onwards: Type controls the dial transport for the
// pair handshake. Address resolution and stored-secret reuse are derived
// from Type.
type PeerJoinParams struct {
	// Type is the user-selected transport (tailscale|local|network|a-sync|repair).
	Type string `json:"type"`
	// Address is the dial target, "ip:port", parsed from the peercode by the
	// CLI before this struct is built. Required for tailscale/local/network.
	// Empty for a-sync (no live handshake) and repair (uses stored secrets).
	Address string `json:"address,omitempty"`
	// Code is the pair-code component of the peercode. Required for
	// tailscale/local/network; empty for a-sync and repair.
	Code string `json:"code,omitempty"`
	// RepoPath is the legacy local-peer hint. Retained for compatibility
	// with the existing `--repo-path` flag; the new `--type local` is the
	// preferred entry point.
	RepoPath string `json:"repo_path,omitempty"`
	// PeerName identifies the existing peer when Type=="repair". Required
	// for repair, ignored for other types.
	PeerName string `json:"peer_name,omitempty"`
	// Remote is the user-supplied git URL for --type a-sync. Empty for
	// other types.
	Remote string `json:"remote,omitempty"`
	// LocalAddress is THIS daemon's LAN IP for --type network. The daemon
	// validates the IP via internal/netdetect, binds a WS listener on
	// LocalAddress:<port>, and uses that listener address as
	// localMeta.Address so the listener-side daemon can reach us back for
	// post-pair sync.notify. Required for --type network; ignored for
	// other types.
	LocalAddress string `json:"local_address,omitempty"`
}

// IsTsnetActive reports whether the daemon's Tailscale tsnet listener is
// already initialized. tsnet only registers a TailscaleSyncInfo provider
// after a successful startTsnet, so a populated, Enabled provider response
// means the daemon already holds a working tsnet node and a fresh auth key
// is not required for further peer pairing on this side.
func IsTsnetActive(health *HealthResult) bool {
	return health != nil && health.Tailscale != nil && health.Tailscale.Enabled
}

// AuthKeyPromptNeeded reports whether `thrum peer add` should prompt the user
// for THRUM_TS_AUTHKEY. It returns false when an auth key is already in the
// caller's environment, or when the local daemon's tsnet is already up
// (in which case the daemon will reuse its cached node credentials).
func AuthKeyPromptNeeded(envAuthKey string, health *HealthResult) bool {
	if envAuthKey != "" {
		return false
	}
	return !IsTsnetActive(health)
}

// PeerStartPairing starts a pairing session on the local daemon.
func PeerStartPairing(client *Client, params *PeerStartPairingParams) (*PeerStartPairingResult, error) {
	var reqParams any = struct{}{}
	if params != nil {
		reqParams = params
	}
	var result PeerStartPairingResult
	if err := client.Call("peer.start_pairing", reqParams, &result); err != nil {
		return nil, fmt.Errorf("start pairing: %w", err)
	}
	return &result, nil
}

// PeerWaitPairing waits for the active pairing session to complete.
// This call blocks until a peer connects or the session times out.
func PeerWaitPairing(client *Client) (*PeerWaitPairingResult, error) {
	var result PeerWaitPairingResult
	// Use a long timeout since this blocks waiting for human interaction
	if err := client.CallWithTimeout("peer.wait_pairing", struct{}{}, &result, 6*time.Minute); err != nil {
		return nil, fmt.Errorf("wait for pairing: %w", err)
	}
	return &result, nil
}

// PeerJoin sends a pair-handshake request to a remote peer using the
// supplied params. From xir.27 onwards Params.Type controls dispatch:
// tailscale/local/network use Address+Code; a-sync uses Remote; repair
// uses PeerName + stored secrets in peers.json. The CLI is responsible
// for parsing the user-supplied peercode into Address+Code and ensuring
// required fields are set per Type.
func PeerJoin(client *Client, params *PeerJoinParams) (*PeerJoinResult, error) {
	if params == nil {
		return nil, fmt.Errorf("peer join: params required")
	}
	req := struct {
		Address      string `json:"address,omitempty"`
		Code         string `json:"code,omitempty"`
		RepoPath     string `json:"repo_path,omitempty"`
		Type         string `json:"type,omitempty"`
		PeerName     string `json:"peer_name,omitempty"`
		Remote       string `json:"remote,omitempty"`
		LocalAddress string `json:"local_address,omitempty"`
	}{
		Address:      params.Address,
		Code:         params.Code,
		RepoPath:     params.RepoPath,
		Type:         params.Type,
		PeerName:     params.PeerName,
		Remote:       params.Remote,
		LocalAddress: params.LocalAddress,
	}

	var result PeerJoinResult
	if err := client.Call("peer.join", req, &result); err != nil {
		return nil, fmt.Errorf("join peer: %w", err)
	}
	return &result, nil
}

// PeerList returns all known peers.
func PeerList(client *Client) ([]PeerListEntry, error) {
	var result []PeerListEntry
	if err := client.Call("peer.list", struct{}{}, &result); err != nil {
		return nil, fmt.Errorf("list peers: %w", err)
	}
	return result, nil
}

// PeerRemove removes a peer by name.
func PeerRemove(client *Client, name string) error {
	req := struct {
		Name string `json:"name"`
	}{Name: name}

	var result map[string]string
	if err := client.Call("peer.remove", req, &result); err != nil {
		return fmt.Errorf("remove peer: %w", err)
	}
	return nil
}

// PeerStatus returns detailed status for all peers.
func PeerStatus(client *Client) ([]PeerDetailedStatusEntry, error) {
	var result []PeerDetailedStatusEntry
	if err := client.Call("peer.status", struct{}{}, &result); err != nil {
		return nil, fmt.Errorf("peer status: %w", err)
	}
	return result, nil
}

// --- Formatting functions ---

// FormatPeerList formats the peer list for display.
func FormatPeerList(peers []PeerListEntry) string {
	if len(peers) == 0 {
		return "No peers paired. Use 'thrum peer add' to start pairing.\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-20s %-22s %-18s %s\n", "NAME", "ADDRESS", "LAST SYNC", "LAST SEQ")

	for _, p := range peers {
		name := p.Name
		if len(name) > 20 {
			name = name[:17] + "..."
		}
		addr := p.Address
		if len(addr) > 22 {
			addr = addr[:19] + "..."
		}

		fmt.Fprintf(&b, "%-20s %-22s %-18s %d\n", name, addr, p.LastSync, p.LastSeq)
	}

	return b.String()
}

// FormatPeerStatus formats detailed peer status for display.
func FormatPeerStatus(peers []PeerDetailedStatusEntry) string {
	if len(peers) == 0 {
		return "No peers paired.\n"
	}

	var b strings.Builder
	for i, p := range peers {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Name:      %s\n", p.Name)
		fmt.Fprintf(&b, "Daemon ID: %s\n", p.DaemonID)
		fmt.Fprintf(&b, "Address:   %s\n", p.Address)
		fmt.Fprintf(&b, "Paired:    %s\n", p.PairedAt)
		fmt.Fprintf(&b, "Last Sync: %s\n", p.LastSync)
		fmt.Fprintf(&b, "Last Seq:  %d\n", p.LastSeq)
		if p.HasToken {
			fmt.Fprintf(&b, "Auth:      token\n")
		} else {
			fmt.Fprintf(&b, "Auth:      none\n")
		}
	}

	return b.String()
}
