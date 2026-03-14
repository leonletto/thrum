package runtime

import "testing"

func TestVerifyBinary_RealBinary(t *testing.T) {
	// "go" binary should exist on any dev machine running these tests
	check := BinaryCheck{
		Name:       "go",
		VerifyArgs: []string{"version"},
		MatchAny:   []string{"go"},
		Timeout:    3000,
	}
	if !verifyBinary(check) {
		t.Error("expected 'go version' to match 'go'")
	}
}

func TestVerifyBinary_NonexistentBinary(t *testing.T) {
	check := BinaryCheck{
		Name:       "nonexistent-binary-xyz-12345",
		VerifyArgs: []string{"--version"},
		MatchAny:   []string{"anything"},
	}
	if verifyBinary(check) {
		t.Error("expected nonexistent binary to fail verification")
	}
}

func TestVerifyBinary_WrongMatch(t *testing.T) {
	// "go version" output won't contain "sourcegraph"
	check := BinaryCheck{
		Name:       "go",
		VerifyArgs: []string{"version"},
		MatchAny:   []string{"sourcegraph"},
		Timeout:    3000,
	}
	if verifyBinary(check) {
		t.Error("expected 'go version' NOT to match 'sourcegraph'")
	}
}

func TestVerifyBinary_DefaultTimeout(t *testing.T) {
	check := BinaryCheck{
		Name:       "go",
		VerifyArgs: []string{"version"},
		MatchAny:   []string{"go"},
		// Timeout: 0 — should use default
	}
	if !verifyBinary(check) {
		t.Error("expected default timeout to work")
	}
}
