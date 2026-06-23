package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// resolveWorktreeBase returns the base ref for a new worktree's branch
// created by `thrum worktree create`. Resolution order:
//
//  1. If baseFlag is non-empty (operator passed --base <ref>), it wins.
//  2. Otherwise, resolve the cwd's current HEAD via
//     `git symbolic-ref --quiet --short HEAD`. On a normal branch this
//     returns the branch name (e.g. "thrum-dev"); the resulting worktree
//     inherits that branch's history.
//  3. On detached HEAD (symbolic-ref errors) or a non-git cwd, the
//     command emits a slog.Warn surfacing the fallback and returns
//     "main". This preserves a working default while making the silent
//     "main" substitution visible — pre-fix the substitution was
//     completely invisible and caused operators to lose commits when
//     they ran the command from non-main branches.
//
// thrum-pqcg.
func resolveWorktreeBase(ctx context.Context, repoPath, baseFlag string) string {
	if baseFlag != "" {
		return baseFlag
	}
	out, err := safecmd.Git(ctx, repoPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	resolved := strings.TrimSpace(string(out))
	if err != nil || resolved == "" {
		slog.Warn("worktree.create: cwd HEAD could not be resolved (detached HEAD or non-git cwd); falling back to base=main. Pass --base to override.")
		return "main"
	}
	return resolved
}
