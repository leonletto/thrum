//go:build unix

package peercred_test

import (
	"errors"
	"net"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
)

// TestResolve_CWDFails_NotAnon verifies that when gopsutil cannot inspect
// the connecting process (permission denied, race against process exit,
// macOS-specific failure modes), the resolver returns a non-ErrAnonymous
// error. Wrapping introspection failure with ErrAnonymous would wrongly
// flip server.go from the "unknown-state → legacy fallthrough" branch
// into the "provably anonymous → allowlist rejection" branch — the exact
// bug thrum-ndtw fixes: interactive shells in a registered worktree got
// their mutating RPCs rejected because gopsutil.Cwd failed on their
// interactive zsh PID.
//
// Test name kept short to stay under the 104-char macOS unix-socket path
// limit that t.TempDir + the test name can blow past.
func TestResolve_CWDFails_NotAnon(t *testing.T) {
	forcedErr := errors.New("gopsutil: forced failure for test")
	restore := peercred.SetProcessCWDFnForTest(func(_ int) (string, error) {
		return "", forcedErr
	})
	defer restore()

	resolver := peercred.NewResolver(&staticLister{agents: nil})

	srv, _, cleanup := makeUnixPair(t)
	defer cleanup()

	_, err := resolver.Resolve(srv)
	if err == nil {
		t.Fatal("expected non-nil error when processCWD fails, got nil")
	}
	if errors.Is(err, peercred.ErrAnonymous) {
		t.Errorf("processCWD failure must NOT wrap ErrAnonymous (would trigger anonymous-allowlist rejection); got: %v", err)
	}
	if !errors.Is(err, forcedErr) {
		t.Errorf("original gopsutil error should remain retrievable via errors.Is; got: %v", err)
	}
}

// TestResolve_PIDFails_NotAnon verifies the same contract for step 1:
// when tailscale/peercred cannot extract peer credentials (e.g. non-unix
// conn like net.Pipe, kernel-level denial), the resolver must return a
// non-ErrAnonymous error. Regression guard — current code already does
// this at the tspeer.Get call site, but the matching PID=0 branch
// historically wrapped with ErrAnonymous and must stay unwrapped after
// thrum-ndtw.
func TestResolve_PIDFails_NotAnon(t *testing.T) {
	resolver := peercred.NewResolver(&staticLister{agents: nil})

	c1, _ := net.Pipe()
	defer c1.Close()

	_, err := resolver.Resolve(c1)
	if err == nil {
		t.Fatal("expected non-nil error when tspeer.Get fails on net.Pipe")
	}
	if errors.Is(err, peercred.ErrAnonymous) {
		t.Errorf("tspeer.Get failure must NOT wrap ErrAnonymous; got: %v", err)
	}
}
