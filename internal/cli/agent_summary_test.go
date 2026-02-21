package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestBuildAgentSummary_FromIdentityFile(t *testing.T) {
	idFile := &config.IdentityFile{
		Version:   3,
		RepoID:    "r_TEST123456",
		Agent:     config.AgentConfig{Name: "coordinator", Role: "coordinator", Module: "main", Display: "Coordinator (main)"},
		Worktree:  "thrum",
		Branch:    "main",
		Intent:    "Coordinate agents and tasks in thrum",
		SessionID: "ses_01ABC",
	}

	summary := BuildAgentSummary(idFile, ".thrum/identities/coordinator.json", nil)

	if summary.AgentID != "coordinator" {
		t.Errorf("AgentID = %q, want %q", summary.AgentID, "coordinator")
	}
	if summary.Role != "coordinator" {
		t.Errorf("Role = %q", summary.Role)
	}
	if summary.Branch != "main" {
		t.Errorf("Branch = %q", summary.Branch)
	}
	if summary.Intent != "Coordinate agents and tasks in thrum" {
		t.Errorf("Intent = %q", summary.Intent)
	}
	if summary.IdentityFile != ".thrum/identities/coordinator.json" {
		t.Errorf("IdentityFile = %q", summary.IdentityFile)
	}
}

func TestBuildAgentSummary_DaemonEnrichment(t *testing.T) {
	idFile := &config.IdentityFile{
		Version:  3,
		Agent:    config.AgentConfig{Name: "coordinator", Role: "coordinator", Module: "main"},
		Worktree: "thrum",
		Branch:   "main",
	}
	daemonInfo := &WhoamiResult{
		AgentID:      "coordinator",
		SessionID:    "ses_LIVE",
		SessionStart: "2026-02-19T12:00:00Z",
	}

	summary := BuildAgentSummary(idFile, ".thrum/identities/coordinator.json", daemonInfo)

	if summary.SessionID != "ses_LIVE" {
		t.Errorf("SessionID = %q, want daemon value %q", summary.SessionID, "ses_LIVE")
	}
	if summary.SessionStart != "2026-02-19T12:00:00Z" {
		t.Errorf("SessionStart = %q", summary.SessionStart)
	}
}

func TestFormatAgentSummary(t *testing.T) {
	summary := &AgentSummary{
		AgentID:      "coordinator",
		Role:         "coordinator",
		Module:       "main",
		Display:      "Coordinator (main)",
		Branch:       "main",
		Intent:       "Coordinate agents and tasks in thrum",
		SessionID:    "ses_01ABC",
		SessionStart: "2026-02-19T12:00:00Z",
		Worktree:     "thrum",
		IdentityFile: ".thrum/identities/coordinator.json",
	}

	output := FormatAgentSummary(summary)

	for _, field := range []string{
		"Agent ID:",
		"coordinator",
		"Role:",
		"Module:",
		"main",
		"Branch:",
		"Intent:",
		"Session:",
		"ses_01ABC",
		"Worktree:",
		"thrum",
		"Identity:",
	} {
		if !strings.Contains(output, field) {
			t.Errorf("Output missing %q:\n%s", field, output)
		}
	}
}

func TestFormatAgentSummaryCompact(t *testing.T) {
	summary := &AgentSummary{
		AgentID: "coordinator",
		Role:    "coordinator",
		Module:  "main",
		Branch:  "main",
		Intent:  "Coordinate agents and tasks in thrum",
		Status:  "active",
	}

	output := FormatAgentSummaryCompact(summary)

	if !strings.Contains(output, "@coordinator") {
		t.Errorf("Compact output missing @coordinator: %q", output)
	}
	if !strings.Contains(output, "main") {
		t.Errorf("Compact output missing branch: %q", output)
	}
}

func TestAgentSummary_JSONParity(t *testing.T) {
	summary := &AgentSummary{
		AgentID:      "coordinator",
		Role:         "coordinator",
		Module:       "main",
		Display:      "Coordinator (main)",
		Branch:       "main",
		Intent:       "Coordinate agents and tasks in thrum",
		Worktree:     "thrum",
		SessionID:    "ses_01ABC",
		IdentityFile: ".thrum/identities/coordinator.json",
	}

	jsonBytes, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}
	jsonStr := string(jsonBytes)

	humanStr := FormatAgentSummary(summary)

	for _, field := range []string{"coordinator", "main", "thrum", "ses_01ABC"} {
		if !strings.Contains(jsonStr, field) {
			t.Errorf("JSON missing %q", field)
		}
		if !strings.Contains(humanStr, field) {
			t.Errorf("Human missing %q", field)
		}
	}
}
