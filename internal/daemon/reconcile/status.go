// Package reconcile implements the xir.29 auto-reconcile peer-drift recovery
// layer. Auto-reconcile fires at daemon boot (one-shot after ConnectAll) and
// inline on send-time drift-indicator errors. It uses secrets already stored
// in peers.json to re-key drifted entries via the peer.repair RPC; the
// listener side of that RPC lives in internal/daemon/repair.go (xir.27).
//
// Auto-reconcile is explicitly NOT periodic — external signal only (boot
// event or send failure). When auto-reconcile cannot resolve drift
// (peer unreachable, stored token rejected), the peer entry is flagged
// with ReconcileStatus=drift_reconcile_failed so `thrum peer list` can
// surface the cue for the user to run `thrum peer join --type repair`.
package reconcile

// Status values written to PeerInfo.ReconcileStatus. Rendered by
// `thrum peer list` so operators see reconcile health at a glance.
const (
	// StatusHealthy is the zero value: no drift detected. Matches the
	// empty-string default of PeerInfo.ReconcileStatus so newly-added
	// peers start healthy without an explicit write.
	StatusHealthy = ""

	// StatusDriftReconcileFailed indicates auto-reconcile attempted to
	// resolve drift for this peer and failed. The failure is terminal
	// until the user runs `thrum peer join --type repair <name>` with
	// the peer's current --address.
	StatusDriftReconcileFailed = "drift_reconcile_failed"
)
