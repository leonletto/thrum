package worktree

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/leonletto/thrum/internal/identity"
)

// derivePathAndBranch computes the worktree path and branch name
// per spec §3.4's naming convention table. The BranchOverride
// field, when non-empty, overrides the convention for the branch
// (but not the path). Internal helper exposed for unit testing.
func derivePathAndBranch(opts CreateOpts) (path, branch string) {
	var leaf string
	var defaultBranch string
	if opts.Persistent {
		leaf = opts.AgentName
		defaultBranch = "agent/" + opts.AgentName
	} else {
		ts := strconv.FormatInt(opts.WakeTimestamp, 10)
		leaf = fmt.Sprintf("%s-%s-%s", opts.AgentName, opts.JobID, ts)
		defaultBranch = fmt.Sprintf("agent/%s/job-%s-%s",
			opts.AgentName, opts.JobID, ts)
	}
	path = filepath.Join(opts.BasePath, leaf)
	if opts.BranchOverride != "" {
		branch = opts.BranchOverride
	} else {
		branch = defaultBranch
	}
	return path, branch
}

// validateOpts checks CreateOpts at API entry per spec §3.4.
// Returns ErrInvalidOpts (wrapped with context) on failure.
//
// AgentName validation delegates to identity.ValidateAgentName so
// the agent-name regex is DRY across the project: lowercase
// letters, digits, underscores, hyphens, colons; rejects reserved
// names like 'daemon', 'system', 'thrum', 'all', 'broadcast'.
func validateOpts(opts CreateOpts) error {
	if opts.RepoPath == "" {
		return fmt.Errorf("%w: RepoPath required", ErrInvalidOpts)
	}
	if err := identity.ValidateAgentName(opts.AgentName); err != nil {
		return fmt.Errorf("%w: AgentName: %v", ErrInvalidOpts, err)
	}
	// Defense-in-depth: identity.ValidateAgentName rejects '/'
	// already (only the agentNameRegex character set passes), but
	// an explicit '..' check is cheap and makes the contract
	// self-evident at the call site.
	if strings.Contains(opts.AgentName, "..") {
		return fmt.Errorf("%w: AgentName %q must not contain parent references",
			ErrInvalidOpts, opts.AgentName)
	}
	if !opts.Persistent {
		if opts.JobID == "" {
			return fmt.Errorf("%w: JobID required when Persistent=false",
				ErrInvalidOpts)
		}
		if opts.WakeTimestamp <= 0 {
			return fmt.Errorf("%w: WakeTimestamp must be > 0 when Persistent=false",
				ErrInvalidOpts)
		}
		// ULID alphabet validation: Crockford Base32 excludes the
		// hyphen by construction. Allow alphanumeric + underscore
		// (the ulid package may produce lowercase or uppercase).
		for _, r := range opts.JobID {
			ok := (r >= '0' && r <= '9') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= 'a' && r <= 'z') ||
				r == '_'
			if !ok {
				return fmt.Errorf("%w: JobID %q contains character %q outside ULID alphabet",
					ErrInvalidOpts, opts.JobID, r)
			}
		}
	}
	// Persistent==true skips JobID/WakeTimestamp checks per spec §3.4.

	// Field-length bound for the 255-byte path-cap test (spec §3.4).
	// The constructive leaf is <agent>-<job>-<ts>; cap each
	// contributor so an over-long single field surfaces as
	// ErrInvalidOpts at validateOpts-time rather than waiting for
	// the constructive path check in Create.
	if len(opts.BasePath) > 200 {
		return fmt.Errorf("%w: BasePath length %d exceeds 200-byte working budget",
			ErrInvalidOpts, len(opts.BasePath))
	}
	return nil
}
