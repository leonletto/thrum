package daemon

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func makeValidEvent() map[string]any {
	return map[string]any{
		"event_id":      "evt_01ABC",
		"type":          "message.create",
		"timestamp":     time.Now().Format(time.RFC3339),
		"origin_daemon": "d_test123",
		"content":       "hello world",
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func newTestValidator(pubKey ed25519.PublicKey, requireSigs bool) *EventValidator {
	return NewEventValidator(func(_ string) ed25519.PublicKey {
		return pubKey
	}, requireSigs)
}

// --- Stage 1: Schema validation ---

func TestValidator_Schema_Valid(t *testing.T) {
	v := newTestValidator(nil, false)
	event := makeValidEvent()
	if err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1"); err != nil {
		t.Errorf("valid event should pass: %v", err)
	}
}

func TestValidator_Schema_MissingFields(t *testing.T) {
	v := newTestValidator(nil, false)

	for _, field := range []string{"event_id", "type", "timestamp", "origin_daemon"} {
		event := makeValidEvent()
		delete(event, field)
		err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1")
		if err == nil {
			t.Errorf("event missing %s should fail", field)
			continue
		}
		valErr, ok := err.(*ValidationError)
		if !ok {
			t.Errorf("expected *ValidationError, got %T", err)
			continue
		}
		if valErr.Stage != "schema" {
			t.Errorf("expected stage 'schema', got %q", valErr.Stage)
		}
		if valErr.Field != field {
			t.Errorf("expected field %q, got %q", field, valErr.Field)
		}
	}
}

func TestValidator_Schema_InvalidType(t *testing.T) {
	v := newTestValidator(nil, false)
	event := makeValidEvent()
	event["type"] = "invalid.type"
	err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1")
	if err == nil {
		t.Fatal("invalid event type should fail")
	}
	valErr := err.(*ValidationError)
	if valErr.Stage != "schema" || valErr.Field != "type" {
		t.Errorf("expected schema/type error, got %s/%s", valErr.Stage, valErr.Field)
	}
}

func TestValidator_Schema_OversizedEvent(t *testing.T) {
	v := newTestValidator(nil, false)
	event := makeValidEvent()
	// Create an event larger than MaxEventSize
	event["payload"] = strings.Repeat("x", MaxEventSize+1)
	err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1")
	if err == nil {
		t.Fatal("oversized event should fail")
	}
	valErr := err.(*ValidationError)
	if valErr.Stage != "schema" {
		t.Errorf("expected stage 'schema', got %q", valErr.Stage)
	}
}

func TestValidator_Schema_InvalidJSON(t *testing.T) {
	v := newTestValidator(nil, false)
	err := v.ValidateIncomingEvent([]byte("{invalid json"), "peer1")
	if err == nil {
		t.Fatal("invalid JSON should fail")
	}
	valErr := err.(*ValidationError)
	if valErr.Stage != "schema" {
		t.Errorf("expected stage 'schema', got %q", valErr.Stage)
	}
}

// --- Stage 2: Signature verification ---

func TestValidator_Signature_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	pub, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys: %v", err)
	}

	v := newTestValidator(pub, false)
	event := makeValidEvent()
	SignEvent(event, priv)

	if err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1"); err != nil {
		t.Errorf("signed event with valid key should pass: %v", err)
	}
}

func TestValidator_Signature_Tampered(t *testing.T) {
	tmpDir := t.TempDir()
	pub, priv, err := EnsureIdentityKeys(tmpDir)
	if err != nil {
		t.Fatalf("EnsureIdentityKeys: %v", err)
	}

	v := newTestValidator(pub, false)
	event := makeValidEvent()
	SignEvent(event, priv)
	event["origin_daemon"] = "d_tampered" // tamper after signing

	err = v.ValidateIncomingEvent(mustMarshal(t, event), "peer1")
	if err == nil {
		t.Fatal("tampered event should fail")
	}
	valErr := err.(*ValidationError)
	if valErr.Stage != "signature" {
		t.Errorf("expected stage 'signature', got %q", valErr.Stage)
	}
}

func TestValidator_Signature_NoSigAccepted(t *testing.T) {
	v := newTestValidator(nil, false) // no signatures required
	event := makeValidEvent()
	// No signature — should be accepted (backward compat)
	if err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1"); err != nil {
		t.Errorf("unsigned event should pass when signatures not required: %v", err)
	}
}

func TestValidator_Signature_RequiredButMissing(t *testing.T) {
	v := newTestValidator(nil, true) // signatures required
	event := makeValidEvent()
	err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1")
	if err == nil {
		t.Fatal("unsigned event should fail when signatures required")
	}
	valErr := err.(*ValidationError)
	if valErr.Stage != "signature" {
		t.Errorf("expected stage 'signature', got %q", valErr.Stage)
	}
}

// --- Stage 3: Business logic ---

func TestValidator_BusinessLogic_FutureTimestamp(t *testing.T) {
	v := newTestValidator(nil, false)
	event := makeValidEvent()
	future := time.Now().Add(25 * time.Hour)
	event["timestamp"] = future.Format(time.RFC3339)

	err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1")
	if err == nil {
		t.Fatal("event with timestamp >24h in future should fail")
	}
	valErr := err.(*ValidationError)
	if valErr.Stage != "business_logic" || valErr.Field != "timestamp" {
		t.Errorf("expected business_logic/timestamp, got %s/%s", valErr.Stage, valErr.Field)
	}
}

func TestValidator_BusinessLogic_PastTimestamp(t *testing.T) {
	v := newTestValidator(nil, false)
	event := makeValidEvent()
	past := time.Now().Add(-48 * time.Hour)
	event["timestamp"] = past.Format(time.RFC3339)

	// Past timestamps should be accepted (events can be old)
	if err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1"); err != nil {
		t.Errorf("event with past timestamp should pass: %v", err)
	}
}

func TestValidator_BusinessLogic_OversizedMessage(t *testing.T) {
	v := newTestValidator(nil, false)
	event := makeValidEvent()
	event["content"] = strings.Repeat("x", MaxMessageSize+1)

	err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1")
	if err == nil {
		t.Fatal("oversized message content should fail")
	}
	valErr := err.(*ValidationError)
	if valErr.Stage != "business_logic" || valErr.Field != "content" {
		t.Errorf("expected business_logic/content, got %s/%s", valErr.Stage, valErr.Field)
	}
}

func TestValidator_BusinessLogic_NormalMessage(t *testing.T) {
	v := newTestValidator(nil, false)
	event := makeValidEvent()
	event["content"] = "short message"

	if err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1"); err != nil {
		t.Errorf("normal size message should pass: %v", err)
	}
}

// --- All event types ---

func TestValidator_AllEventTypes(t *testing.T) {
	v := newTestValidator(nil, false)
	for eventType := range validEventTypes {
		event := map[string]any{
			"event_id":      "evt_01ABC",
			"type":          eventType,
			"timestamp":     time.Now().Format(time.RFC3339),
			"origin_daemon": "d_test123",
		}
		if err := v.ValidateIncomingEvent(mustMarshal(t, event), "peer1"); err != nil {
			t.Errorf("event type %q should be valid: %v", eventType, err)
		}
	}
}
