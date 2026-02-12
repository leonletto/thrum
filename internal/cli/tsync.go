package cli

import (
	"fmt"
	"strings"
)

// TsyncForceRequest represents a request to trigger Tailscale peer sync.
type TsyncForceRequest struct {
	From string `json:"from,omitempty"`
}

// TsyncForceResult is the result of a Tailscale sync operation.
type TsyncForceResult struct {
	Status  string           `json:"status"`
	Message string           `json:"message,omitempty"`
	Results []map[string]any `json:"results,omitempty"`
}

// TsyncForce triggers sync from Tailscale peers.
func TsyncForce(client *Client, from string) (*TsyncForceResult, error) {
	req := TsyncForceRequest{From: from}

	var result TsyncForceResult
	if err := client.Call("tsync.force", req, &result); err != nil {
		return nil, fmt.Errorf("tsync.force RPC failed: %w", err)
	}

	return &result, nil
}

// TsyncPeerStatus represents a peer's status for display.
type TsyncPeerStatus struct {
	DaemonID string `json:"daemon_id"`
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	LastSeen string `json:"last_seen"`
	Status   string `json:"status"`
	LastSeq  int64  `json:"last_synced_seq"`
}

// TsyncPeersList returns known Tailscale peers.
func TsyncPeersList(client *Client) ([]TsyncPeerStatus, error) {
	var result []TsyncPeerStatus
	if err := client.Call("tsync.peers.list", struct{}{}, &result); err != nil {
		return nil, fmt.Errorf("tsync.peers.list RPC failed: %w", err)
	}
	return result, nil
}

// TsyncPeersAdd adds a peer manually.
func TsyncPeersAdd(client *Client, hostname string, port int) error {
	req := struct {
		Hostname string `json:"hostname"`
		Port     int    `json:"port"`
	}{Hostname: hostname, Port: port}

	var result map[string]string
	if err := client.Call("tsync.peers.add", req, &result); err != nil {
		return fmt.Errorf("tsync.peers.add RPC failed: %w", err)
	}
	return nil
}

// FormatTsyncForce formats the tsync force result for display.
func FormatTsyncForce(result *TsyncForceResult) string {
	if result.Status == "no_peers" {
		return "No peers configured. Add peers with: thrum daemon peers add <hostname>\n"
	}

	var b strings.Builder
	for _, r := range result.Results {
		peer, _ := r["peer"].(string)
		applied, _ := r["applied"].(float64)
		skipped, _ := r["skipped"].(float64)
		errMsg, hasErr := r["error"].(string)

		if hasErr {
			fmt.Fprintf(&b, "  %s: error — %s\n", peer, errMsg)
		} else {
			fmt.Fprintf(&b, "  %s: %d applied, %d skipped\n", peer, int(applied), int(skipped))
		}
	}

	return b.String()
}

// FormatTsyncPeersList formats the peer list for display.
func FormatTsyncPeersList(peers []TsyncPeerStatus) string {
	if len(peers) == 0 {
		return "No peers configured.\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-20s %-20s %-18s %-10s %s\n",
		"DAEMON ID", "HOSTNAME", "LAST SEEN", "STATUS", "LAST SEQ")

	for _, p := range peers {
		daemonID := p.DaemonID
		if len(daemonID) > 20 {
			daemonID = daemonID[:17] + "..."
		}
		hostname := p.Hostname
		if len(hostname) > 20 {
			hostname = hostname[:17] + "..."
		}

		fmt.Fprintf(&b, "%-20s %-20s %-18s %-10s %d\n",
			daemonID, hostname, p.LastSeen, p.Status, p.LastSeq)
	}

	return b.String()
}
