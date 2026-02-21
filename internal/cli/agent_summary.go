package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// AgentSummary is the canonical representation of an agent's identity and state.
// Used by whoami, team, agent list, status, overview.
// JSON mode marshals this directly; human mode uses FormatAgentSummary.
type AgentSummary struct {
	AgentID      string `json:"agent_id"`
	Role         string `json:"role"`
	Module       string `json:"module"`
	Display      string `json:"display,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Worktree     string `json:"worktree,omitempty"`
	Intent       string `json:"intent,omitempty"`
	RepoID       string `json:"repo_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	SessionStart string `json:"session_start,omitempty"`
	IdentityFile string `json:"identity_file,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	Source       string `json:"source"`
	Status       string `json:"status,omitempty"`
}

// BuildAgentSummary constructs an AgentSummary from an identity file and
// optional daemon info. Identity file is the base; daemon enriches with
// live session data.
func BuildAgentSummary(idFile *config.IdentityFile, idPath string, daemonInfo *WhoamiResult) *AgentSummary {
	s := &AgentSummary{
		AgentID:      idFile.Agent.Name,
		Role:         idFile.Agent.Role,
		Module:       idFile.Agent.Module,
		Display:      idFile.Agent.Display,
		Branch:       idFile.Branch,
		Worktree:     idFile.Worktree,
		Intent:       idFile.Intent,
		RepoID:       idFile.RepoID,
		SessionID:    idFile.SessionID,
		IdentityFile: idPath,
		Source:       "file",
	}

	if !idFile.UpdatedAt.IsZero() {
		s.UpdatedAt = idFile.UpdatedAt.Format(time.RFC3339)
	}

	// Enrich from daemon if available
	if daemonInfo != nil {
		s.Source = "daemon"
		if daemonInfo.SessionID != "" {
			s.SessionID = daemonInfo.SessionID
		}
		if daemonInfo.SessionStart != "" {
			s.SessionStart = daemonInfo.SessionStart
		}
		if daemonInfo.Display != "" {
			s.Display = daemonInfo.Display
		}
		if daemonInfo.Branch != "" {
			s.Branch = daemonInfo.Branch
		}
		if daemonInfo.Intent != "" {
			s.Intent = daemonInfo.Intent
		}
	}

	return s
}

// FormatAgentSummary formats an AgentSummary for multi-line human-readable
// display. Used by whoami, status, overview for the "self" section.
func FormatAgentSummary(s *AgentSummary) string {
	var out strings.Builder

	out.WriteString(fmt.Sprintf("Agent ID:  %s\n", s.AgentID))
	out.WriteString(fmt.Sprintf("Role:      @%s\n", s.Role))

	if s.Module != "" {
		out.WriteString(fmt.Sprintf("Module:    %s\n", s.Module))
	}
	if s.Display != "" {
		out.WriteString(fmt.Sprintf("Display:   %s\n", s.Display))
	}
	if s.Branch != "" {
		out.WriteString(fmt.Sprintf("Branch:    %s\n", s.Branch))
	}
	if s.Intent != "" {
		out.WriteString(fmt.Sprintf("Intent:    %s\n", s.Intent))
	}

	if s.SessionID != "" {
		sessionAge := ""
		if s.SessionStart != "" {
			if t, err := time.Parse(time.RFC3339, s.SessionStart); err == nil {
				sessionAge = fmt.Sprintf(" (%s ago)", formatDuration(time.Since(t)))
			}
		}
		out.WriteString(fmt.Sprintf("Session:   %s%s\n", s.SessionID, sessionAge))
	} else {
		out.WriteString("Session:   none (use 'thrum session start' to begin)\n")
	}

	if s.Worktree != "" {
		out.WriteString(fmt.Sprintf("Worktree:  %s\n", s.Worktree))
	}
	if s.IdentityFile != "" {
		out.WriteString(fmt.Sprintf("Identity:  %s\n", s.IdentityFile))
	}

	return out.String()
}

// FormatAgentSummaryCompact formats an AgentSummary as a single-line summary.
// Used in team and agent list contexts.
// Format: "● @name (module) — intent [branch]"
func FormatAgentSummaryCompact(s *AgentSummary) string {
	icon := "○"
	if s.Status == "active" {
		icon = "●"
	}

	parts := []string{fmt.Sprintf("%s @%s (%s)", icon, s.AgentID, s.Module)}

	if s.Intent != "" {
		parts = append(parts, fmt.Sprintf("— %s", s.Intent))
	}
	if s.Branch != "" {
		parts = append(parts, fmt.Sprintf("[%s]", s.Branch))
	}

	return strings.Join(parts, " ")
}
