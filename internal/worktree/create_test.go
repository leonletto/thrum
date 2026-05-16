package worktree

import "testing"

func TestDerivePathAndBranch(t *testing.T) {
	cases := []struct {
		name           string
		opts           CreateOpts
		wantPathSuffix string // path relative to BasePath
		wantBranch     string
	}{
		{
			name: "ephemeral basic",
			opts: CreateOpts{
				BasePath:      "/tmp/wt",
				AgentName:     "docs_bot",
				JobID:         "job_01HABCDE",
				WakeTimestamp: 1715731200,
				Persistent:    false,
			},
			wantPathSuffix: "docs_bot-job_01HABCDE-1715731200",
			wantBranch:     "agent/docs_bot/job-job_01HABCDE-1715731200",
		},
		{
			name: "persistent basic",
			opts: CreateOpts{
				BasePath:   "/tmp/wt",
				AgentName:  "docs_bot",
				Persistent: true,
			},
			wantPathSuffix: "docs_bot",
			wantBranch:     "agent/docs_bot",
		},
		{
			name: "ephemeral with BranchOverride",
			opts: CreateOpts{
				BasePath:       "/tmp/wt",
				AgentName:      "x",
				JobID:          "j",
				WakeTimestamp:  1,
				Persistent:     false,
				BranchOverride: "feature/x",
			},
			wantPathSuffix: "x-j-1",
			wantBranch:     "feature/x",
		},
		{
			name: "persistent with BranchOverride",
			opts: CreateOpts{
				BasePath:       "/tmp/wt",
				AgentName:      "x",
				Persistent:     true,
				BranchOverride: "feature/x",
			},
			wantPathSuffix: "x",
			wantBranch:     "feature/x",
		},
		{
			name: "persistent without BranchOverride uses agent/<name> default",
			opts: CreateOpts{
				BasePath:   "/tmp/wt",
				AgentName:  "docs_bot",
				Persistent: true,
				// BranchOverride intentionally empty — verifies the
				// default agent/<AgentName> convention fires (Leon
				// Q1 was about cobra-side default; this is the
				// headless API default).
			},
			wantPathSuffix: "docs_bot",
			wantBranch:     "agent/docs_bot",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPath, gotBranch := derivePathAndBranch(c.opts)
			wantPath := c.opts.BasePath + "/" + c.wantPathSuffix
			if gotPath != wantPath {
				t.Errorf("path: got %q, want %q", gotPath, wantPath)
			}
			if gotBranch != c.wantBranch {
				t.Errorf("branch: got %q, want %q", gotBranch, c.wantBranch)
			}
		})
	}
}
