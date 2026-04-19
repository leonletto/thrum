package reconcile

import (
	"context"
	"errors"
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
