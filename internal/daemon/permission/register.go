package permission

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/leonletto/thrum/internal/config"
)

// ResolveProjectName returns the project name for the supervisor sender
// identity. Prefers config.ProjectName, falls back to filepath.Base of
// repoPath, and ultimately returns "project" if neither yields a usable
// value.
func ResolveProjectName(cfg *config.ThrumConfig, repoPath string) string {
	if cfg != nil && cfg.ProjectName != "" {
		return cfg.ProjectName
	}
	if repoPath != "" {
		base := filepath.Base(repoPath)
		if base != "" && base != "." && base != "/" {
			return base
		}
	}
	return "project"
}

// SupervisorAgentID derives the full agent ID for the supervisor
// pseudo-agent from the project name.
func SupervisorAgentID(projectName string) string {
	return "supervisor_" + projectName
}

// RegisterSupervisor ensures the @supervisor_<project> pseudo-agent exists
// in the local identities directory with Reserved=true. Safe to call
// repeatedly at daemon boot — a second call rewrites the same identity
// file in place.
//
// The supervisor pseudo-agent is intentionally NOT registered via a state
// event: sendSystemMessage (internal/daemon/rpc/queue_rpc.go) already
// demonstrates that the messages table accepts sender agent_ids that do
// not exist in the agents table (e.g. "system"). The supervisor follows
// the same pattern. This means IsAgentActive("supervisor_<project>") will
// always return false — correct behavior, since the supervisor must never
// appear as a recipient in permission_supervisors (nudging yourself about
// your own nudges is nonsensical), and that falsey active check enforces
// the invariant if someone misconfigures.
func RegisterSupervisor(ctx context.Context, cfg *config.ThrumConfig, thrumDir, repoPath string) (string, error) {
	projectName := ResolveProjectName(cfg, repoPath)
	agentID := SupervisorAgentID(projectName)

	idFile := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    agentID,
			Role:    "supervisor",
			Module:  "daemon",
			Display: "Supervisor (" + projectName + ")",
		},
		Reserved: true,
	}

	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		return "", fmt.Errorf("save supervisor identity: %w", err)
	}

	return agentID, nil
}
