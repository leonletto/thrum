package cli

import (
	"fmt"
	"strings"
)

// QuickstartOptions contains options for the quickstart command.
type QuickstartOptions struct {
	Name       string
	Role       string
	Module     string
	Display    string
	Intent     string
	ReRegister bool
}

// QuickstartResult contains the combined result of quickstart steps.
type QuickstartResult struct {
	Register *RegisterResponse     `json:"register"`
	Session  *SessionStartResponse `json:"session"`
	Intent   *SetIntentResponse    `json:"intent,omitempty"`
}

// Quickstart registers an agent, starts a session, and optionally sets intent.
func Quickstart(client *Client, opts QuickstartOptions) (*QuickstartResult, error) {
	result := &QuickstartResult{}

	// Step 1: Register agent
	regOpts := AgentRegisterOptions{
		Name:    opts.Name,
		Role:    opts.Role,
		Module:  opts.Module,
		Display: opts.Display,
	}

	regResult, err := AgentRegister(client, regOpts)
	if err != nil {
		return nil, fmt.Errorf("register failed: %w", err)
	}

	// If conflict, try re-register automatically
	if regResult.Status == "conflict" {
		regOpts.ReRegister = true
		regResult, err = AgentRegister(client, regOpts)
		if err != nil {
			return nil, fmt.Errorf("re-register failed: %w", err)
		}
	}

	result.Register = regResult

	// Step 2: Start session
	sessOpts := SessionStartOptions{
		AgentID: regResult.AgentID,
	}

	sessResult, err := SessionStart(client, sessOpts)
	if err != nil {
		return nil, fmt.Errorf("session start failed: %w", err)
	}
	result.Session = sessResult

	// Step 3: Set intent (optional)
	if opts.Intent != "" {
		intentResult, err := SessionSetIntent(client, sessResult.SessionID, opts.Intent)
		if err != nil {
			return nil, fmt.Errorf("set intent failed: %w", err)
		}
		result.Intent = intentResult
	}

	return result, nil
}

// FormatQuickstart formats the quickstart result for display.
func FormatQuickstart(result *QuickstartResult) string {
	var output strings.Builder

	// Registration
	output.WriteString(fmt.Sprintf("✓ Registered as @%s (%s)\n",
		extractRoleFromID(result.Register.AgentID), result.Register.AgentID))

	// Session
	output.WriteString(fmt.Sprintf("✓ Session started: %s\n", result.Session.SessionID))

	// Intent
	if result.Intent != nil && result.Intent.Intent != "" {
		output.WriteString(fmt.Sprintf("✓ Intent set: %s\n", result.Intent.Intent))
	}

	return output.String()
}

// extractRoleFromID extracts the role from an agent ID (agent:role:module).
func extractRoleFromID(agentID string) string {
	parts := strings.Split(agentID, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return agentID
}
