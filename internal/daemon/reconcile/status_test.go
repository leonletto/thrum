package reconcile

import "testing"

func TestStatusConstants(t *testing.T) {
	if StatusHealthy != "" {
		t.Errorf("StatusHealthy must be empty string (matches zero-value PeerInfo.ReconcileStatus); got %q", StatusHealthy)
	}
	if StatusDriftReconcileFailed != "drift_reconcile_failed" {
		t.Errorf("StatusDriftReconcileFailed = %q, want drift_reconcile_failed", StatusDriftReconcileFailed)
	}
}
