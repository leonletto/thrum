package monitor

import (
	"context"
	"encoding/json"
	"fmt"
)

// MessageSender is the minimal subset of the MessageHandler API needed for
// delivery. Defined as an interface so tests can substitute a fake without
// importing internal/daemon/rpc (which would create an import cycle).
//
// In production, *rpc.MessageHandler satisfies this interface because its
// HandleSend method has the signature (context.Context, json.RawMessage) (any, error).
type MessageSender interface {
	HandleSend(ctx context.Context, params json.RawMessage) (any, error)
}

// Delivery constructs synthetic thrum messages from monitor matches and
// submits them to the existing MessageSender pipeline. This guarantees
// that monitor matches flow through the same storage, subscription,
// notification, and tmux-nudge paths as user messages — no parallel
// delivery code.
type Delivery struct {
	sender MessageSender
}

// NewDelivery creates a Delivery that routes through the given MessageSender.
func NewDelivery(sender MessageSender) *Delivery {
	return &Delivery{sender: sender}
}

// sendPayload mirrors the fields of rpc.SendRequest that Delivery needs.
// Using a local struct avoids an import cycle and keeps the interface minimal.
type sendPayload struct {
	Content       string `json:"content"`
	CallerAgentID string `json:"caller_agent_id"`
	Mentions      []string `json:"mentions,omitempty"`
}

// Deliver builds a synthetic message with sender "monitor:<monitorName>" and
// submits it through the MessageSender. The target is passed as a mention so
// the existing mention-resolution logic routes the message to the right agent.
// If target is empty, the message is sent as a broadcast with no explicit recipient.
func (d *Delivery) Deliver(ctx context.Context, monitorName, target, content string) error {
	payload := sendPayload{
		Content:       content,
		CallerAgentID: "monitor:" + monitorName,
	}
	if target != "" {
		payload.Mentions = []string{target}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("monitor delivery: marshal params: %w", err)
	}
	if _, err := d.sender.HandleSend(ctx, raw); err != nil {
		return fmt.Errorf("monitor delivery: handle send: %w", err)
	}
	return nil
}
