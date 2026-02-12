package cli

import (
	"fmt"
	"strings"
	"time"
)

// --- Request/Response types (mirror rpc package) ---

// PeerStartPairingResult is the result of starting a pairing session.
type PeerStartPairingResult struct {
	Code string `json:"code"`
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

// PeerStartPairing starts a pairing session on the local daemon.
func PeerStartPairing(client *Client) (*PeerStartPairingResult, error) {
	var result PeerStartPairingResult
	if err := client.Call("peer.start_pairing", struct{}{}, &result); err != nil {
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

// PeerJoin sends a pairing code to a remote peer.
func PeerJoin(client *Client, address, code string) (*PeerJoinResult, error) {
	req := struct {
		Address string `json:"address"`
		Code    string `json:"code"`
	}{Address: address, Code: code}

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
