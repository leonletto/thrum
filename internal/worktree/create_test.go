package worktree

import (
	"errors"
	"strings"
	"testing"
)

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

func TestValidateOpts(t *testing.T) {
	cases := []struct {
		name    string
		opts    CreateOpts
		wantErr error // nil for pass; ErrInvalidOpts for fail
	}{
		{
			name: "valid ephemeral",
			opts: CreateOpts{
				RepoPath: "/repo", BasePath: "/wt",
				AgentName: "docs_bot", JobID: "j01", WakeTimestamp: 1,
				Persistent: false,
			},
			wantErr: nil,
		},
		{
			name: "valid persistent",
			opts: CreateOpts{
				RepoPath: "/repo", BasePath: "/wt",
				AgentName:  "docs_bot",
				Persistent: true,
			},
			wantErr: nil,
		},
		{
			name:    "empty RepoPath",
			opts:    CreateOpts{AgentName: "x", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "empty AgentName",
			opts:    CreateOpts{RepoPath: "/r", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName with slash",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "x/y", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName with ..",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "..", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName UPPER (rejected by identity.ValidateAgentName)",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "DOCS_BOT", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName with bang (rejected by identity.ValidateAgentName)",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "name!", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName 'daemon' (reserved by identity.ValidateAgentName)",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "daemon", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "ephemeral missing JobID",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "x", Persistent: false, WakeTimestamp: 1},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "ephemeral missing WakeTimestamp",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "x", Persistent: false, JobID: "j"},
			wantErr: ErrInvalidOpts,
		},
		{
			name: "persistent ignores JobID/WakeTimestamp",
			opts: CreateOpts{
				RepoPath: "/r", BasePath: "/wt", AgentName: "x",
				Persistent: true,
				// JobID and WakeTimestamp are zero-valued; per
				// spec §3.4 validation must SKIP these fields
				// when Persistent == true.
			},
			wantErr: nil,
		},
		{
			name: "resulting path > 255 bytes (spec §3.4 cap)",
			opts: CreateOpts{
				RepoPath: "/r",
				// 256-char BasePath forces leaf computation past
				// the 255-byte filesystem cap; ErrInvalidOpts at
				// API entry per spec §3.4 path-length guard.
				BasePath:  strings.Repeat("a", 256),
				AgentName: "x", Persistent: true,
			},
			wantErr: ErrInvalidOpts,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateOpts(c.opts)
			if c.wantErr == nil && err != nil {
				t.Errorf("got err %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Errorf("got err %v, want errors.Is(%v) true", err, c.wantErr)
			}
		})
	}
}
