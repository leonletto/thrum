// tmux_test_seam.go: cross-package test seams. Cannot live in a _test.go
// file because internal/daemon/bootstrap's tests import these symbols at
// the package boundary. Linker dead-code-elimination keeps them out of
// the production binary (verified: not in `go tool nm bin/thrum`).
package rpc

// This file holds test-only seams for cross-package tests
// (e.g. internal/daemon/bootstrap) that need to construct or inspect
// a TmuxHandler without going through production wiring. Names are
// suffixed with ForTest / ForBootstrap to make non-production use
// obvious to readers and reviewers.

// NewTestTmuxHandlerForBootstrap constructs a TmuxHandler with empty
// in-memory maps. Test-only — not for use in production code.
func NewTestTmuxHandlerForBootstrap() *TmuxHandler {
	return &TmuxHandler{
		sessionCwds: make(map[string]string),
		cwdSessions: make(map[string]string),
	}
}

// TmuxHandlerGetBindingForTest returns sessionCwds[name] under the
// handler's read lock. Test-only.
func TmuxHandlerGetBindingForTest(h *TmuxHandler, name string) string {
	h.sessionMu.RLock()
	defer h.sessionMu.RUnlock()
	return h.sessionCwds[name]
}
