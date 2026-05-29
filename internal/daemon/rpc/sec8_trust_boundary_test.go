package rpc

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// TestSec8TrustBoundary_BulkDeleteNotOnWebSocket is a structural guard test
// that asserts message.deleteByAgent and message.deleteByScope are NOT
// registered on the WebSocket transport (wsRegistry).
//
// These are admin/system bulk-hard-delete operations (sec.8). The unix-socket
// transport restricts them via peercred-based checks, but the WS transport
// has no peercred injection — any localhost browser page could invoke them
// if they were registered on wsRegistry.
//
// This test mirrors the monitor trust boundary pattern in
// monitor_trust_boundary_test.go (source-scan layer).
//
// IF THIS TEST FAILS
// Someone added a wsRegistry.Register("message.deleteByAgent", ...) Or
// wsRegistry.Register("message.deleteByScope", ...) Line to cmd/thrum/main.go.
// DO NOT fix the test — remove the registration. These methods must only be
// on the unix-socket server object.
func TestSec8TrustBoundary_BulkDeleteNotOnWebSocket(t *testing.T) {
	forbidden := []string{
		"message.deleteByAgent",
		"message.deleteByScope",
	}

	mainGoPath := repoRootRelative(t, "cmd/thrum/main.go")
	f, err := os.Open(mainGoPath)
	if err != nil {
		t.Fatalf("cannot open cmd/thrum/main.go: %v", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if !strings.Contains(line, "wsRegistry.Register(") {
			continue
		}
		for _, method := range forbidden {
			if strings.Contains(line, `"`+method+`"`) {
				t.Errorf(
					"SECURITY: cmd/thrum/main.go:%d registers %q on wsRegistry:\n\t%s\n"+
						"Bulk hard-delete methods must only be on the unix-socket server.\n"+
						"See sec.8 (thrum-u4xv.8) for why this is dangerous.",
					lineNum, method, strings.TrimSpace(line),
				)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("error scanning main.go: %v", err)
	}
}

// TestRestartClass_TmuxCreateNotOnWebSocket is the load-bearing safety
// invariant for thrum-5oui Option A. tmux.create was added to the daemon's
// anonymous-allowlist (internal/daemon/server.go) so an UNBOUND caller can
// bootstrap an agent via `thrum tmux start` (the agent-restart entry). That is
// safe ONLY because tmux.create is reachable solely over the local 0600
// owner-only unix socket — it is NOT registered on wsRegistry, so the
// peer/tsnet WS transport (which has no peercred injection → every caller is
// anonymous) cannot reach it. If tmux.create were ever registered on
// wsRegistry, allowlisting it would let any localhost browser page / remote
// peer create tmux sessions anonymously — a real hole.
//
// IF THIS TEST FAILS
// Someone added a wsRegistry.Register("tmux.create", ...) line to
// cmd/thrum/main.go. DO NOT fix the test — remove the registration, OR remove
// tmux.create from anonymousAllowedMethods. The two are mutually exclusive:
// tmux.create may be anonymous-allowed (unix only) XOR WS-registered, never
// both. See server.go's tmux.create allowlist comment + thrum-5oui.
func TestRestartClass_TmuxCreateNotOnWebSocket(t *testing.T) {
	assertNotRegisteredOnWebSocket(t, "tmux.create")
}

func TestRestartClass_TmuxLaunchNotOnWebSocket(t *testing.T) {
	assertNotRegisteredOnWebSocket(t, "tmux.launch")
}

// assertNotRegisteredOnWebSocket source-scans cmd/thrum/main.go and fails if
// `method` is registered on wsRegistry. Both tmux.create and tmux.launch are in
// the anonymous allowlist for the `thrum tmux start` bootstrap (thrum-5oui),
// which is safe ONLY because they live solely on the local 0600 unix socket.
// The WS/peer transport has no peercred injection (every caller anonymous), so
// a WS registration of either would let a remote peer / localhost browser
// create sessions + launch runtimes anonymously — a real hole.
//
// IF THIS TEST FAILS: someone added wsRegistry.Register("<method>", ...) to
// cmd/thrum/main.go. DO NOT fix the test — remove the registration, OR remove
// the method from anonymousAllowedMethods (server.go). The two are mutually
// exclusive.
func assertNotRegisteredOnWebSocket(t *testing.T, method string) {
	t.Helper()
	mainGoPath := repoRootRelative(t, "cmd/thrum/main.go")
	f, err := os.Open(mainGoPath)
	if err != nil {
		t.Fatalf("cannot open cmd/thrum/main.go: %v", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if !strings.Contains(line, "wsRegistry.Register(") {
			continue
		}
		if strings.Contains(line, `"`+method+`"`) {
			t.Errorf(
				"SECURITY (thrum-5oui): cmd/thrum/main.go:%d registers %s on wsRegistry:\n\t%s\n"+
					"%s is anonymous-allowed (unix-socket bootstrap for `thrum tmux start`).\n"+
					"It MUST NOT be on the WS/peer transport — that would allow anonymous bootstrap over the network.",
				lineNum, method, strings.TrimSpace(line), method,
			)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("error scanning main.go: %v", err)
	}
}
