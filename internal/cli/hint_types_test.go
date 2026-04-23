package cli

import "testing"

func TestSeverityValues(t *testing.T) {
	if SeverityWarn != "warn" {
		t.Errorf("SeverityWarn = %q, want warn", SeverityWarn)
	}
	if SeverityInfo != "info" {
		t.Errorf("SeverityInfo = %q, want info", SeverityInfo)
	}
}

func TestIdentityStatusValues(t *testing.T) {
	if IdentityNone != 0 || IdentityStale != 1 || IdentityLive != 2 {
		t.Errorf("IdentityStatus ordering changed: none=%d stale=%d live=%d",
			IdentityNone, IdentityStale, IdentityLive)
	}
}

func TestHintZeroValueAllowForceIsFalse(t *testing.T) {
	h := Hint{}
	if h.AllowForce {
		t.Error("zero-value Hint.AllowForce should be false (hard refusal default)")
	}
}
