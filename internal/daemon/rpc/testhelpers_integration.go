//go:build integration

package rpc

// SetSessionCwdForTest wires a session→cwd mapping without going
// through HandleCreate. Integration tests need this to exercise the
// launch path without spinning up a full daemon bootstrap + git
// worktree setup.
//
// Gated by the `integration` build tag so the production binary never
// exposes this seam. Callers (tests/integration/*) must run with
// `go test -tags=integration`.
func SetSessionCwdForTest(h *TmuxHandler, sessionName, cwd string) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	if h.sessionCwds == nil {
		h.sessionCwds = make(map[string]string)
	}
	h.sessionCwds[sessionName] = cwd
}
