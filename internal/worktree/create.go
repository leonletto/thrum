package worktree

import (
	"fmt"
	"path/filepath"
	"strconv"
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
