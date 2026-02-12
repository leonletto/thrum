package daemon

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityFreshKeyGeneration(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "identity-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Ensure keys are generated
	pub, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys failed: %v", err)
	}

	// Verify key types
	if pub == nil {
		t.Fatal("public key is nil")
	}
	if priv == nil {
		t.Fatal("private key is nil")
	}

	// Verify key sizes
	if len(pub) != ed25519.PublicKeySize {
		t.Errorf("public key size = %d, want %d", len(pub), ed25519.PublicKeySize)
	}
	if len(priv) != ed25519.PrivateKeySize {
		t.Errorf("private key size = %d, want %d", len(priv), ed25519.PrivateKeySize)
	}

	// Verify public key derivation
	derivedPub := priv.Public().(ed25519.PublicKey)
	if !derivedPub.Equal(pub) {
		t.Error("public key does not match derived public key from private key")
	}

	// Verify file exists with correct permissions
	keyPath := filepath.Join(tmpDir, "identity.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("key file does not exist: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("key file permissions = %o, want 0600", perm)
	}
}

func TestIdentityKeyLoading(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "identity-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Generate keys first time
	pub1, priv1, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys (first call) failed: %v", err)
	}

	fingerprint1 := PublicKeyFingerprint(pub1)

	// Load keys second time (should load existing keys)
	pub2, priv2, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys (second call) failed: %v", err)
	}

	fingerprint2 := PublicKeyFingerprint(pub2)

	// Verify keys are identical
	if !pub1.Equal(pub2) {
		t.Error("loaded public key differs from original")
	}
	if !priv1.Equal(priv2) {
		t.Error("loaded private key differs from original")
	}
	if fingerprint1 != fingerprint2 {
		t.Errorf("fingerprints differ: %s vs %s", fingerprint1, fingerprint2)
	}
}

func TestIdentityPublicKeyDerivation(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "identity-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Generate keys
	pub, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys failed: %v", err)
	}

	// Derive public key from private key
	derivedPub := priv.Public().(ed25519.PublicKey)

	// Verify they match
	if !pub.Equal(derivedPub) {
		t.Error("public key does not match key derived from private key")
	}

	// Verify we can sign and verify with the keys
	message := []byte("test message for signing")
	signature := ed25519.Sign(priv, message)

	if !ed25519.Verify(pub, message, signature) {
		t.Error("signature verification failed")
	}

	if !ed25519.Verify(derivedPub, message, signature) {
		t.Error("signature verification with derived public key failed")
	}
}

func TestIdentityFingerprintFormat(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "identity-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Generate keys
	pub, _, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys failed: %v", err)
	}

	// Get fingerprint
	fingerprint := PublicKeyFingerprint(pub)

	// Verify format: "SHA256:base64string"
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Errorf("fingerprint does not start with 'SHA256:': %s", fingerprint)
	}

	// Verify the base64 part is non-empty
	parts := strings.SplitN(fingerprint, ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		t.Errorf("fingerprint base64 part is empty: %s", fingerprint)
	}

	// Verify length (SHA256 produces 32 bytes, base64 encodes to 44 chars)
	// Plus "SHA256:" prefix (7 chars) = 51 chars total
	if len(fingerprint) != 51 {
		t.Errorf("fingerprint length = %d, want 51 (SHA256: + 44 base64 chars)", len(fingerprint))
	}
}

func TestIdentityDirectoryCreation(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "identity-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a nested path that doesn't exist
	nestedDir := filepath.Join(tmpDir, "nested", "state")

	// Ensure keys are generated (should create parent directories)
	_, _, err = EnsureIdentityKeys(nestedDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys with nested dir failed: %v", err)
	}

	// Verify directory was created
	info, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("nested directory was not created: %v", err)
	}

	if !info.IsDir() {
		t.Error("nested path is not a directory")
	}

	// Verify directory permissions (0700)
	perm := info.Mode().Perm()
	if perm != 0700 {
		t.Errorf("directory permissions = %o, want 0700", perm)
	}
}

func TestIdentityConsistency(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "identity-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Generate keys multiple times in sequence
	var fingerprints []string
	for i := range 3 {
		pub, _, err := EnsureIdentityKeys(tmpDir)
		if err != nil {
			t.Fatalf("EnsureIdentityKeys (iteration %d) failed: %v", i, err)
		}
		fingerprints = append(fingerprints, PublicKeyFingerprint(pub))
	}

	// All fingerprints should be identical (keys loaded from disk)
	for i := 1; i < len(fingerprints); i++ {
		if fingerprints[i] != fingerprints[0] {
			t.Errorf("fingerprint mismatch at iteration %d: %s vs %s", i, fingerprints[i], fingerprints[0])
		}
	}
}

func TestSignEvent(t *testing.T) {
	tmpDir := t.TempDir()
	pub, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys failed: %v", err)
	}

	event := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     "2026-01-01T00:00:00Z",
		"origin_daemon": "d_test123",
	}

	// Sign the event
	SignEvent(event, priv)

	// Verify signature field was added
	sigStr, ok := event["signature"].(string)
	if !ok || sigStr == "" {
		t.Fatal("signature field not set after SignEvent")
	}

	// Decode and verify the signature
	sigBytes, err := base64.StdEncoding.DecodeString(sigStr)
	if err != nil {
		t.Fatalf("failed to decode signature: %v", err)
	}

	payload := CanonicalSigningPayload(event)
	if !ed25519.Verify(pub, []byte(payload), sigBytes) {
		t.Error("signature verification failed")
	}
}

func TestSignEventDeterministic(t *testing.T) {
	tmpDir := t.TempDir()
	_, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys failed: %v", err)
	}

	// Ed25519 signing is deterministic — same input = same signature
	event1 := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     "2026-01-01T00:00:00Z",
		"origin_daemon": "d_test123",
	}
	event2 := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     "2026-01-01T00:00:00Z",
		"origin_daemon": "d_test123",
	}

	SignEvent(event1, priv)
	SignEvent(event2, priv)

	if event1["signature"] != event2["signature"] {
		t.Error("same event content should produce identical signatures")
	}
}

func TestSignEventNilKey(t *testing.T) {
	event := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     "2026-01-01T00:00:00Z",
		"origin_daemon": "d_test123",
	}

	// SignEvent with nil key should be a no-op
	SignEvent(event, nil)

	if _, exists := event["signature"]; exists {
		t.Error("signature should not be set when private key is nil")
	}
}

func TestCanonicalSigningPayload(t *testing.T) {
	event := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     "2026-01-01T00:00:00Z",
		"origin_daemon": "d_test123",
		"extra_field":   "ignored",
	}

	payload := CanonicalSigningPayload(event)
	expected := "evt_01ABC|message.create|2026-01-01T00:00:00Z|d_test123"
	if payload != expected {
		t.Errorf("canonical payload = %q, want %q", payload, expected)
	}
}
