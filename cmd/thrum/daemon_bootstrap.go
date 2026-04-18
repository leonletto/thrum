package main

import (
	"log/slog"

	"github.com/leonletto/thrum/internal/identity/guard"
)

// guardDaemonBootstrap refuses `thrum daemon run` / `thrum daemon start`
// from a non-git directory unless --force is passed. Same footgun-closure
// as `thrum init` G2: ensures every daemon anchors to a git repo so
// identity files derive from git state rather than ad-hoc cwd names.
//
// Mode is loaded from identity_guard.non_git_bootstrap in the .thrum/
// config under repoPath; missing or unset mode falls back to strict so
// enforcement defaults on.
func guardDaemonBootstrap(repoPath string, force bool, warnLogger *slog.Logger) error {
	mode := guard.LoadConfigFromDir(repoPath).NonGitBootstrap
	if mode == "" {
		mode = guard.ModeStrict
	}
	return guard.G2(mode, repoPath, force, warnLogger)
}
