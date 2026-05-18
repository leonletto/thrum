// Package escalation routes B-B1 / D-B1 / A-B4 alert events to the
// operator via the right delivery channel per spec §8.
//
// The package is consumed by five escalation sites in the v0.11
// substrate: idle-nudge exhaustion (B-B1 E6.4), stage-failure
// 3-consecutive (B-B1 E6.1), auto-respawn loop guard (B-B1 E6.7),
// state.md parse failure (B-B1 E6.5), and nudge target offline
// (B-B1 E6.3). Owning the routing decision here keeps each call site
// free of the email-vs-supervisor branch logic.
//
// RouteEscalation is the package-level entry point; Deps is the
// dependency-injection seam so daemon-boot wiring can supply real
// EmailRPC + MessageRPC implementations while tests swap in fakes.
package escalation

import (
	"context"
	"fmt"
	"log/slog"
)

// Alert tags the source of an escalation so downstream tooling
// (operator inbox grep, email subject filtering) can route at a
// glance. Source values are the canonical strings from spec §8:
// "b-b1.idle_nudge", "b-b1.stage_failure", "b-b1.auto_respawn_loop_guard",
// "b-b1.state_md_parse_failure", "b-b1.nudge_target_offline".
type Alert struct {
	Source    string
	AgentName string
	JobID     string
	RunID     string
}

// EmailRPC is the minimal email-send surface RouteEscalation reaches
// for when email is the configured channel. Concrete implementation
// ships from internal/bridge/email (D-B1); tests inject a fake.
//
// Transient bridge failures (network blip, queue offline) are
// absorbed at this layer — the D-B1 queue handles retry, so
// RouteEscalation returns nil after logging the error.
type EmailRPC interface {
	Send(ctx context.Context, recipient, subject, body string) error
}

// MessageRPC is the minimal message-send surface RouteEscalation
// falls back on when email isn't configured. Concrete implementation
// ships from internal/daemon/rpc.MessageHandler; tests inject a fake.
// Subject + body get composed into a single message body so the
// supervisor agent's inbox parser doesn't need to split them.
type MessageRPC interface {
	MessageSend(ctx context.Context, target, subject, body string) (string, error)
}

// Config carries the operator-configurable knobs RouteEscalation
// reads on every call. Loaded once at daemon-boot wiring time from
// the thrum config tree.
type Config struct {
	// EmailEnabled toggles the email route. When false, all
	// escalations fall back to the supervisor agent path.
	EmailEnabled bool

	// OperatorAddress is the destination address for the email
	// route. Required when EmailEnabled is true; ignored otherwise.
	OperatorAddress string

	// SupervisorAgentName is the fallback recipient when email
	// isn't configured. Empty string falls back to "coordinator"
	// (the canonical default per spec §8).
	SupervisorAgentName string
}

// Deps carries the dependency-injection points RouteEscalation
// uses. Mirrors the B-B1 agentdispatch.Deps pattern: interfaces
// for swappable backends, struct config for static knobs.
type Deps struct {
	Email   EmailRPC
	Message MessageRPC
	Config  Config
}

// emailConfigured reports whether the email route is wired up + the
// operator address is present. Both must be true for a Send attempt;
// otherwise the supervisor fallback fires.
func (d Deps) emailConfigured() bool {
	return d.Config.EmailEnabled && d.Email != nil && d.Config.OperatorAddress != ""
}

// RouteEscalation delivers an alert to the operator via the
// configured channel. Returns nil on success; returns the underlying
// MessageRPC error only when the supervisor fallback itself fails
// (the email path absorbs transient errors since D-B1's queue handles
// retry).
func RouteEscalation(ctx context.Context, alert Alert, subject, body string, deps Deps) error {
	if deps.emailConfigured() {
		if err := deps.Email.Send(ctx, deps.Config.OperatorAddress, subject, body); err != nil {
			slog.Warn("escalation: email.send returned error; D-B1 queue handles retry",
				"err", err,
				"source", alert.Source,
				"agent", alert.AgentName,
				"job_id", alert.JobID,
				"run_id", alert.RunID,
			)
			return nil // success — queued
		}
		return nil
	}

	supervisor := deps.Config.SupervisorAgentName
	if supervisor == "" {
		supervisor = "coordinator"
	}
	if deps.Message == nil {
		return fmt.Errorf("escalation: no Email + no Message dep wired (alert source=%q)", alert.Source)
	}
	composed := subject + "\n\n" + body
	_, err := deps.Message.MessageSend(ctx, supervisor, subject, composed)
	return err
}
