package identity

import "testing"

func TestIsLegacyHostnameDerivedID(t *testing.T) {
	host := "leonsmacm1pro"
	legacy := legacyHostnameDerivedID(host)
	if !IsLegacyDaemonID(legacy, host) {
		t.Fatalf("legacyHostnameDerivedID output %q not recognized as legacy", legacy)
	}
	ulid := GenerateDaemonID()
	if IsLegacyDaemonID(ulid, host) {
		t.Fatalf("fresh ULID %q incorrectly classified as legacy", ulid)
	}
	if IsLegacyDaemonID("", host) {
		t.Fatalf("empty id classified as legacy")
	}
}
