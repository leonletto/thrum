package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/leonletto/thrum/internal/bridge"
)

// DialerIdentity is the local identity payload sent in the peer.repair
// request. Field shape matches internal/daemon.PairMetadata so the wire
// format is stable across the xir.27 repair RPC and this xir.29 caller.
type DialerIdentity struct {
	DaemonID     string
	Address      string
	RepoName     string
	Hostname     string
	RepoPath     string
	GitOriginURL string
}

// RepairResponse is the listener's answer to peer.repair. Mirrors
// internal/daemon/rpc.peerRepairResponse — we deliberately redeclare it
// here to avoid a daemon→rpc import cycle.
type RepairResponse struct {
	DaemonID     string `json:"daemon_id"`
	Name         string `json:"name"`
	RepoName     string `json:"repo_name,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	RepoPath     string `json:"repo_path,omitempty"`
	GitOriginURL string `json:"git_origin_url,omitempty"`
}

// DialFunc dials a peer, issues peer.repair with (token, local), and
// returns the listener's current identity. Errors surface via
// ErrUnreachable / ErrTokenRejected sentinels; CategorizeErr classifies
// the result for the calling reconcile manager.
type DialFunc func(ctx context.Context, address, token string, local DialerIdentity) (RepairResponse, error)

// ErrCategory groups dial/repair failures so the reconcile manager can
// decide (a) whether to mark the peer drift_reconcile_failed (terminal
// failures) vs treat the error as transient (skip marking, retry later)
// and (b) whether the send-time OnDialError hook should attempt an
// immediate retry or fall through to normal backoff.
type ErrCategory int

const (
	// CatOK is the success category (err == nil).
	CatOK ErrCategory = iota

	// CatUnreachable means the TCP/WS dial never completed. The stored
	// address is stale and we have no discovery mechanism to find the
	// new address; escalate to manual `thrum peer join --type repair`.
	CatUnreachable

	// CatTokenRejected means the WS connected but peer.repair returned
	// "no matching peer". Stored token is no longer valid — identities
	// have diverged. Escalate to manual `thrum peer join --type repair`.
	CatTokenRejected

	// CatOther is any other failure (context cancel, unexpected RPC
	// error, unmarshal failure). Treated as transient: do NOT mark
	// drift_reconcile_failed, log and let the caller retry on next
	// trigger.
	CatOther
)

var (
	// ErrUnreachable is the sentinel for dial-time failures (connection
	// refused, no route, DNS fail, timeout). Wrap it with fmt.Errorf
	// "%w" so errors.Is picks it up.
	ErrUnreachable = errors.New("reconcile: peer unreachable at stored address")

	// ErrTokenRejected is the sentinel for authentication failures from
	// peer.repair ("no matching peer"). Wrap with fmt.Errorf "%w".
	ErrTokenRejected = errors.New("reconcile: stored token rejected by peer")
)

// WSDial is the production DialFunc. It opens a one-shot WS connection
// to the peer's `/ws` endpoint, presents the stored bearer token, issues
// a single peer.repair RPC, and closes. Does NOT maintain a persistent
// bridge — the caller (reconcile.Manager) only needs the one-shot
// identity-verification side-effect.
//
// Error handling:
//   - Dial failure (TCP/WS) → wrapped ErrUnreachable (→ CatUnreachable).
//   - peer.repair returning "no matching peer" → wrapped ErrTokenRejected
//     (→ CatTokenRejected). The listener's terse error message is
//     documented in internal/daemon/repair.go:81.
//   - Any other RPC failure → wrapped plain error (→ CatOther).
func WSDial(ctx context.Context, address, token string, local DialerIdentity) (RepairResponse, error) {
	url := "ws://" + address + "/ws"
	ws := bridge.NewWSClient(url, bridge.WithBearerToken(token))
	if err := ws.Connect(ctx); err != nil {
		return RepairResponse{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = ws.Close() }()

	params := map[string]any{
		"token":          token,
		"daemon_id":      local.DaemonID,
		"address":        local.Address,
		"repo_name":      local.RepoName,
		"hostname":       local.Hostname,
		"repo_path":      local.RepoPath,
		"git_origin_url": local.GitOriginURL,
	}
	raw, err := ws.Call(ctx, "peer.repair", params)
	if err != nil {
		if strings.Contains(err.Error(), "no matching peer") {
			return RepairResponse{}, fmt.Errorf("%w: %v", ErrTokenRejected, err)
		}
		return RepairResponse{}, fmt.Errorf("peer.repair call: %w", err)
	}
	var resp RepairResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return RepairResponse{}, fmt.Errorf("unmarshal peer.repair response: %w", err)
	}
	return resp, nil
}

// CategorizeErr maps a dial error to an ErrCategory using errors.Is so
// wrapped errors classify correctly.
func CategorizeErr(err error) ErrCategory {
	switch {
	case err == nil:
		return CatOK
	case errors.Is(err, ErrUnreachable):
		return CatUnreachable
	case errors.Is(err, ErrTokenRejected):
		return CatTokenRejected
	default:
		return CatOther
	}
}
