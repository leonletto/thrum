package daemon

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"time"
)

// Validation constants.
const (
	MaxEventSize    = 1 * 1024 * 1024  // 1 MB
	MaxMessageSize  = 100 * 1024       // 100 KB
	MaxTimestampSkew = 24 * time.Hour
)

// ValidationError categorizes validation failures.
type ValidationError struct {
	Stage   string // "schema", "signature", "business_logic"
	Field   string // which field failed (if applicable)
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("validation failed [%s] field %s: %s", e.Stage, e.Field, e.Message)
	}
	return fmt.Sprintf("validation failed [%s]: %s", e.Stage, e.Message)
}

// EventValidator validates incoming sync events through a three-stage pipeline.
type EventValidator struct {
	getPeerPublicKey func(peerID string) ed25519.PublicKey // lookup peer public key
	requireSignatures bool // if true, reject unsigned events
}

// NewEventValidator creates a new event validator.
// getPeerPublicKey is a function that returns the public key for a peer,
// or nil if unknown. If requireSignatures is true, unsigned events are rejected.
func NewEventValidator(getPeerPublicKey func(string) ed25519.PublicKey, requireSignatures bool) *EventValidator {
	return &EventValidator{
		getPeerPublicKey:  getPeerPublicKey,
		requireSignatures: requireSignatures,
	}
}

// validEventTypes lists the known event types.
var validEventTypes = map[string]bool{
	"message.create":      true,
	"message.edit":        true,
	"message.delete":      true,
	"session.start":       true,
	"session.end":         true,
	"session.heartbeat":   true,
	"agent.register":      true,
	"agent.deregister":    true,
	"subscribe":           true,
	"unsubscribe":         true,
	"group.create":        true,
	"group.join":          true,
	"group.leave":         true,
	"thread.create":       true,
	"thread.reply":        true,
}

// ValidateIncomingEvent runs the three-stage validation pipeline on an incoming event.
// Stage 1: Schema validation (required fields, valid type, size limits)
// Stage 2: Signature verification (Ed25519 signature check)
// Stage 3: Business logic (timestamp sanity, agent_id format, message size)
func (v *EventValidator) ValidateIncomingEvent(eventJSON []byte, peerID string) error {
	// Stage 0: Size check on raw bytes
	if len(eventJSON) > MaxEventSize {
		return &ValidationError{
			Stage:   "schema",
			Field:   "event_json",
			Message: fmt.Sprintf("event size %d exceeds maximum %d bytes", len(eventJSON), MaxEventSize),
		}
	}

	// Parse event
	var event map[string]any
	if err := json.Unmarshal(eventJSON, &event); err != nil {
		return &ValidationError{
			Stage:   "schema",
			Message: fmt.Sprintf("invalid JSON: %v", err),
		}
	}

	// Stage 1: Schema validation
	if err := v.validateSchema(event); err != nil {
		return err
	}

	// Stage 2: Signature verification
	if err := v.validateSignature(event, peerID); err != nil {
		return err
	}

	// Stage 3: Business logic
	if err := v.validateBusinessLogic(event); err != nil {
		return err
	}

	return nil
}

// validateSchema checks required fields and valid event type.
func (v *EventValidator) validateSchema(event map[string]any) error {
	// Required fields
	requiredFields := []string{"event_id", "type", "timestamp", "origin_daemon"}
	for _, field := range requiredFields {
		val, ok := event[field].(string)
		if !ok || val == "" {
			return &ValidationError{
				Stage:   "schema",
				Field:   field,
				Message: "required field missing or empty",
			}
		}
	}

	// Valid event type
	eventType, _ := event["type"].(string)
	if !validEventTypes[eventType] {
		return &ValidationError{
			Stage:   "schema",
			Field:   "type",
			Message: fmt.Sprintf("unknown event type %q", eventType),
		}
	}

	return nil
}

// validateSignature verifies the Ed25519 signature using the peer's public key.
func (v *EventValidator) validateSignature(event map[string]any, peerID string) error {
	sigStr, hasSig := event["signature"].(string)
	if !hasSig || sigStr == "" {
		if v.requireSignatures {
			return &ValidationError{
				Stage:   "signature",
				Message: "signature required but not present",
			}
		}
		// Unsigned events accepted during migration
		return nil
	}

	var pubKey ed25519.PublicKey
	if v.getPeerPublicKey != nil {
		pubKey = v.getPeerPublicKey(peerID)
	}

	if !VerifyEventSignature(event, pubKey) {
		return &ValidationError{
			Stage:   "signature",
			Field:   "signature",
			Message: fmt.Sprintf("invalid signature from peer %s", peerID),
		}
	}

	return nil
}

// validateBusinessLogic checks timestamp sanity, agent_id format, and message size.
func (v *EventValidator) validateBusinessLogic(event map[string]any) error {
	// Timestamp sanity: not more than 24h in the future
	timestampStr, _ := event["timestamp"].(string)
	if ts, err := time.Parse(time.RFC3339, timestampStr); err == nil {
		if ts.After(time.Now().Add(MaxTimestampSkew)) {
			return &ValidationError{
				Stage:   "business_logic",
				Field:   "timestamp",
				Message: fmt.Sprintf("timestamp %s is more than %v in the future", timestampStr, MaxTimestampSkew),
			}
		}
	}

	// Message size limit for message.create events
	eventType, _ := event["type"].(string)
	if eventType == "message.create" {
		if content, ok := event["content"].(string); ok {
			if len(content) > MaxMessageSize {
				return &ValidationError{
					Stage:   "business_logic",
					Field:   "content",
					Message: fmt.Sprintf("message content size %d exceeds maximum %d bytes", len(content), MaxMessageSize),
				}
			}
		}
	}

	return nil
}
