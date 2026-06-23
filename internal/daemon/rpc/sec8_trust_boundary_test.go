package rpc

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scanCmdThrumForWSRegistration source-scans EVERY non-test .go file under
// cmd/thrum for a `wsRegistry.Register("<method>", ...)` call and fails if it
// finds one for any forbidden method.
//
// Scanning the whole directory — rather than a hardcoded filename — is
// deliberate and load-bearing. cmd/thrum is monolithic on the release line
// (all wsRegistry.Register calls live in main.go) but decomposed on the
// development line (registrations split across daemon_run.go, daemon_tmux.go,
// daemon_handlers.go, …). A hardcoded-file scan develops a silent blind spot
// the moment a registration moves to a file the scan doesn't name — exactly
// the gap thrum-5oui's review caught (the dev-line guard scanned daemon_run.go
// but not daemon_tmux.go, where the tmux registrations actually live). A
// directory scan can never drift: it always covers the file a registration
// actually lands in, regardless of cmd/thrum's decomposition state, and keeps
// this guard identical on both lines.
//
// Returns the per-method human-readable description appended to any failure so
// callers can explain WHY the registration is forbidden.
func scanCmdThrumForWSRegistration(t *testing.T, secLabel string, methods map[string]string) {
	t.Helper()

	dir := repoRootRelative(t, "cmd/thrum")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot read cmd/thrum: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		path := filepath.Join(dir, name)
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("cannot open %s: %v", path, err)
		}

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			// Case-insensitive so the single matcher covers both the local
			// var form (`wsRegistry.Register(`) and the struct-field form
			// (`deps.WsRegistry.Register(`) that coexist across the
			// monolithic/decomposed cmd/thrum layouts. Deliberately NOT a
			// broad `Registry.Register(` — that would also match the
			// peer/sync `syncRegistry.Register(` calls. `RegisterHandler`
			// (the unix-socket registration) is naturally excluded.
			if !strings.Contains(strings.ToLower(line), "wsregistry.register(") {
				continue
			}
			for method, why := range methods {
				if strings.Contains(line, `"`+method+`"`) {
					t.Errorf(
						"SECURITY (%s): cmd/thrum/%s:%d registers %q on wsRegistry:\n\t%s\n%s",
						secLabel, name, lineNum, method, strings.TrimSpace(line), why,
					)
				}
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			_ = f.Close()
			t.Fatalf("error scanning %s: %v", path, scanErr)
		}
		_ = f.Close()
	}
}

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
// Someone added a wsRegistry.Register("message.deleteByAgent", ...) or
// wsRegistry.Register("message.deleteByScope", ...) line somewhere under
// cmd/thrum. DO NOT fix the test — remove the registration. These methods must
// only be on the unix-socket server object.
func TestSec8TrustBoundary_BulkDeleteNotOnWebSocket(t *testing.T) {
	why := "Bulk hard-delete methods must only be on the unix-socket server.\n" +
		"See sec.8 (thrum-u4xv.8) for why this is dangerous."
	scanCmdThrumForWSRegistration(t, "sec.8", map[string]string{
		"message.deleteByAgent": why,
		"message.deleteByScope": why,
	})
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
// Someone added a wsRegistry.Register("tmux.create", ...) line somewhere under
// cmd/thrum. DO NOT fix the test — remove the registration, OR remove
// tmux.create from anonymousAllowedMethods. The two are mutually exclusive:
// tmux.create may be anonymous-allowed (unix only) XOR WS-registered, never
// both. See server.go's tmux.create allowlist comment + thrum-5oui.
func TestRestartClass_TmuxCreateNotOnWebSocket(t *testing.T) {
	assertNotRegisteredOnWebSocket(t, "tmux.create")
}

func TestRestartClass_TmuxLaunchNotOnWebSocket(t *testing.T) {
	assertNotRegisteredOnWebSocket(t, "tmux.launch")
}

// assertNotRegisteredOnWebSocket source-scans every non-test .go file under
// cmd/thrum (via scanCmdThrumForWSRegistration) and fails if `method` is
// registered on wsRegistry. Both tmux.create and tmux.launch are in the
// anonymous allowlist for the `thrum tmux start` bootstrap (thrum-5oui), which
// is safe ONLY because they live solely on the local 0600 unix socket. The
// WS/peer transport has no peercred injection (every caller anonymous), so a
// WS registration of either would let a remote peer / localhost browser create
// sessions + launch runtimes anonymously — a real hole.
//
// IF THIS TEST FAILS: someone added wsRegistry.Register("<method>", ...)
// somewhere under cmd/thrum. DO NOT fix the test — remove the registration, OR
// remove the method from anonymousAllowedMethods (server.go). The two are
// mutually exclusive.
func assertNotRegisteredOnWebSocket(t *testing.T, method string) {
	t.Helper()
	scanCmdThrumForWSRegistration(t, "thrum-5oui", map[string]string{
		method: method + " is anonymous-allowed (unix-socket bootstrap for `thrum tmux start`).\n" +
			"It MUST NOT be on the WS/peer transport — that would allow anonymous bootstrap over the network.",
	})
}
