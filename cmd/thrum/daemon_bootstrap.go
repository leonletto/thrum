package main

import (
	"log/slog"

	"github.com/leonletto/thrum/internal/identity/guard"
)

// guardDaemonBootstrap refuses `thrum daemon run` / `thrum daemon start`
// from a non-git directory unless --force is passed. Same footgun-closure
// as `thrum init`. See guard.G2 for guard semantics and mode
// documentation. Mode is loaded from identity_guard.non_git_bootstrap
// under repoPath; missing or unset mode falls back to strict so
// enforcement defaults on.
func guardDaemonBootstrap(repoPath string, force bool, warnLogger *slog.Logger) error {
	mode := guard.LoadConfigFromDir(repoPath).NonGitBootstrap
	if mode == "" {
		mode = guard.ModeStrict
	}
	return guard.G2(mode, repoPath, force, warnLogger)
}
