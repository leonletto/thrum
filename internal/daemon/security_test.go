package daemon

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// --- Key Generation and Persistence ---

func TestSecurity_KeyGeneration_Fresh(t *testing.T) {
	tmpDir := t.TempDir()
	pub, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Errorf("public key size = %d, want %d", len(pub), ed25519.PublicKeySize)
	}
	if len(priv) != ed25519.PrivateKeySize {
		t.Errorf("private key size = %d, want %d", len(priv), ed25519.PrivateKeySize)
	}
}

func TestSecurity_KeyGeneration_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	pub1, _, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	pub2, _, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if !pub1.Equal(pub2) {
		t.Error("keys should be identical across loads")
	}
}

func TestSecurity_KeyPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	_, _, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys: %v", err)
	}

	info, err := os.Stat(tmpDir + "/identity.key")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}

// --- End-to-End Signing + Verification ---

func TestSecurity_EndToEnd_SignAndVerify(t *testing.T) {
	tmpDir := t.TempDir()
	pub, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys: %v", err)
	}

	event := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     time.Now().Format(time.RFC3339),
		"origin_daemon": "d_test123",
		"content":       "hello world",
	}

	// Sign
	SignEvent(event, priv)
	sig, ok := event["signature"].(string)
	if !ok || sig == "" {
		t.Fatal("signature not set")
	}

	// Verify
	if !VerifyEventSignature(event, pub) {
		t.Error("valid signature should verify")
	}

	// Tamper
	event["content"] = "tampered"
	// Note: content is NOT part of canonical payload, so this shouldn't affect verification
	if !VerifyEventSignature(event, pub) {
		t.Error("changing non-canonical field should not affect signature")
	}

	// Tamper canonical field
	event["type"] = "message.delete"
	if VerifyEventSignature(event, pub) {
		t.Error("changing canonical field should invalidate signature")
	}
}

func TestSecurity_SignatureDeterminism(t *testing.T) {
	tmpDir := t.TempDir()
	_, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys: %v", err)
	}

	makeEvent := func() map[string]any {
		return map[string]any{
			"event_id":      "evt_deterministic",
			"type":          "session.start",
			"timestamp":     "2026-01-01T00:00:00Z",
			"origin_daemon": "d_test",
		}
	}

	e1 := makeEvent()
	e2 := makeEvent()
	SignEvent(e1, priv)
	SignEvent(e2, priv)

	if e1["signature"] != e2["signature"] {
		t.Error("deterministic: same event should produce same signature")
	}
}

// --- Validation Pipeline End-to-End ---

func TestSecurity_ValidationPipeline_ValidSigned(t *testing.T) {
	tmpDir := t.TempDir()
	pub, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys: %v", err)
	}

	v := NewEventValidator(func(_ string) ed25519.PublicKey { return pub }, false)

	event := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     time.Now().Format(time.RFC3339),
		"origin_daemon": "d_test123",
		"content":       "hello",
	}
	SignEvent(event, priv)
	data, _ := json.Marshal(event)

	if err := v.ValidateIncomingEvent(data, "d_test123"); err != nil {
		t.Errorf("valid signed event should pass: %v", err)
	}
}

func TestSecurity_ValidationPipeline_TamperedSignature(t *testing.T) {
	tmpDir := t.TempDir()
	pub, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys: %v", err)
	}

	v := NewEventValidator(func(_ string) ed25519.PublicKey { return pub }, false)

	event := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     time.Now().Format(time.RFC3339),
		"origin_daemon": "d_test123",
	}
	SignEvent(event, priv)
	event["origin_daemon"] = "d_tampered" // tamper after signing
	data, _ := json.Marshal(event)

	err = v.ValidateIncomingEvent(data, "d_test123")
	if err == nil {
		t.Fatal("tampered signature should fail")
	}
	valErr := err.(*ValidationError)
	if valErr.Stage != "signature" {
		t.Errorf("expected stage 'signature', got %q", valErr.Stage)
	}
}

func TestSecurity_ValidationPipeline_UnsignedAccepted(t *testing.T) {
	v := NewEventValidator(nil, false) // no sigs required

	event := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     time.Now().Format(time.RFC3339),
		"origin_daemon": "d_test123",
	}
	data, _ := json.Marshal(event)

	if err := v.ValidateIncomingEvent(data, "peer1"); err != nil {
		t.Errorf("unsigned event should be accepted: %v", err)
	}
}

func TestSecurity_ValidationPipeline_UnsignedRejected(t *testing.T) {
	v := NewEventValidator(nil, true) // sigs required

	event := map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     time.Now().Format(time.RFC3339),
		"origin_daemon": "d_test123",
	}
	data, _ := json.Marshal(event)

	err := v.ValidateIncomingEvent(data, "peer1")
	if err == nil {
		t.Fatal("unsigned event should be rejected when sigs required")
	}
}

// --- Quarantine Integration ---

func TestSecurity_QuarantineIntegration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	q, err := NewQuarantineStore(db)
	if err != nil {
		t.Fatalf("NewQuarantineStore: %v", err)
	}

	v := NewEventValidator(nil, true) // require signatures

	// Create unsigned event — should fail validation
	event := map[string]any{
		"event_id":      "evt_quarantine",
		"type":          "message.create",
		"timestamp":     time.Now().Format(time.RFC3339),
		"origin_daemon": "d_test",
	}
	data, _ := json.Marshal(event)

	err = v.ValidateIncomingEvent(data, "d_malicious")
	if err == nil {
		t.Fatal("should have failed validation")
	}

	// Quarantine the invalid event
	err = q.Quarantine("evt_quarantine", "d_malicious", err.Error(), string(data))
	if err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	// Verify it's in quarantine
	events, err := q.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 quarantined event, got %d", len(events))
	}
	if events[0].FromDaemon != "d_malicious" {
		t.Errorf("FromDaemon = %q, want %q", events[0].FromDaemon, "d_malicious")
	}
}

// --- Rate Limiting ---

func TestSecurity_RateLimiter_BurstAndRecovery(t *testing.T) {
	rl := NewSyncRateLimiter(RateLimitConfig{
		MaxRequestsPerSecond: 100,
		BurstSize:            3,
		MaxSyncQueueDepth:    100,
		Enabled:              true,
	})

	// Burst of 3 should succeed
	for i := range 3 {
		if err := rl.Allow("peer1"); err != nil {
			t.Errorf("burst %d should succeed: %v", i, err)
		}
	}

	// 4th should fail with 429
	err := rl.Allow("peer1")
	if err == nil {
		t.Fatal("should be rate limited")
	}
	rlErr := err.(*RateLimitError)
	if rlErr.Code != 429 {
		t.Errorf("code = %d, want 429", rlErr.Code)
	}

	// Different peer should still work
	if err := rl.Allow("peer2"); err != nil {
		t.Errorf("different peer should not be affected: %v", err)
	}
}

func TestSecurity_RateLimiter_QueueOverload(t *testing.T) {
	rl := NewSyncRateLimiter(RateLimitConfig{
		MaxRequestsPerSecond: 100,
		BurstSize:            100,
		MaxSyncQueueDepth:    2,
		Enabled:              true,
	})

	rl.IncrementQueue()
	rl.IncrementQueue()

	err := rl.Allow("peer1")
	if err == nil {
		t.Fatal("should return 503 when queue full")
	}
	rlErr := err.(*RateLimitError)
	if rlErr.Code != 503 {
		t.Errorf("code = %d, want 503", rlErr.Code)
	}
}

// --- WhoIs Authorization ---

func TestSecurity_Authorization_AllChecks(t *testing.T) {
	auth := NewSyncAuthorizer(SyncAuthConfig{
		AllowedPeers:  []string{"server-1", "server-2"},
		RequiredTags:  []string{"tag:thrum-daemon"},
		AllowedDomain: "@company.com",
		RequireAuth:   true,
	})

	// All checks pass
	if err := auth.AuthorizePeer("server-1", []string{"tag:thrum-daemon"}, "alice@company.com"); err != nil {
		t.Errorf("should be authorized: %v", err)
	}

	// Wrong hostname
	if err := auth.AuthorizePeer("unknown", []string{"tag:thrum-daemon"}, "alice@company.com"); err == nil {
		t.Error("wrong hostname should fail")
	}

	// Wrong tag
	if err := auth.AuthorizePeer("server-1", []string{"tag:wrong"}, "alice@company.com"); err == nil {
		t.Error("wrong tag should fail")
	}

	// Wrong domain
	if err := auth.AuthorizePeer("server-1", []string{"tag:thrum-daemon"}, "alice@other.com"); err == nil {
		t.Error("wrong domain should fail")
	}
}

func TestSecurity_Authorization_DisabledMode(t *testing.T) {
	auth := NewSyncAuthorizer(SyncAuthConfig{RequireAuth: false})

	// Any peer should pass when auth disabled
	if err := auth.AuthorizePeer("anything", nil, ""); err != nil {
		t.Errorf("should pass when auth disabled: %v", err)
	}
}

// --- Private Key Security ---

func TestSecurity_PrivateKeyNotExposed(t *testing.T) {
	tmpDir := t.TempDir()
	_, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys: %v", err)
	}

	event := map[string]any{
		"event_id":      "evt_01",
		"type":          "message.create",
		"timestamp":     time.Now().Format(time.RFC3339),
		"origin_daemon": "d_test",
	}
	SignEvent(event, priv)

	// Ensure private key material is NOT in the event
	data, _ := json.Marshal(event)
	eventStr := string(data)
	privB64 := base64.StdEncoding.EncodeToString(priv)

	if len(privB64) > 20 {
		// Check that a significant chunk of the private key isn't in the event
		privSubstring := privB64[:20]
		for i := range len(eventStr) - 20 {
			if eventStr[i:i+20] == privSubstring {
				t.Error("private key material found in serialized event")
			}
		}
	}
}

// --- Cross-Key Verification ---

func TestSecurity_CrossKeyVerificationFails(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	_, priv1, _ := EnsureIdentityKeys(dir1)
	pub2, _, _ := EnsureIdentityKeys(dir2)

	event := map[string]any{
		"event_id":      "evt_cross",
		"type":          "message.create",
		"timestamp":     time.Now().Format(time.RFC3339),
		"origin_daemon": "d_test",
	}
	SignEvent(event, priv1)

	// Verify with wrong key must fail
	if VerifyEventSignature(event, pub2) {
		t.Error("verification with wrong key should fail")
	}
}
