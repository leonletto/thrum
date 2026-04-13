package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/websocket"
)

// TestMonitorTrustBoundary_NotOnWebSocket asserts that no monitor.* method is
// exposed over the WebSocket / peer transport.
//
// Monitor submission must remain local-unix-socket-only because it spawns
// child processes with the daemon's privileges. A remote peer that can call
// monitor.start would be able to execute arbitrary commands on the daemon
// host — a critical privilege escalation.
//
// This test guards the design decision in:
//
//	dev-docs/specs/2026-04-11-monitor-jobs-design.md § "Trust boundary"
//
// HOW IT WORKS
// The test has two complementary layers:
//
//  1. Runtime layer: creates a fresh SimpleRegistry (the same type used for
//     wsRegistry in cmd/thrum/main.go), registers a representative sample
//     of legitimate WebSocket methods (to confirm the enumeration mechanism
//     works), and asserts that none of the registered methods has the
//     "monitor." prefix. Since no monitor handlers are wired up yet, this
//     layer passes trivially.
//
//  2. Source-scan layer: reads cmd/thrum/main.go and scans every
//     wsRegistry.Register(...) call site to assert none contain "monitor."
//     as the first argument. This layer will catch the exact commit that
//     would introduce the vulnerability, even if the runtime layer somehow
//     misses it.
//
// IF THIS TEST FAILS
// Someone added a wsRegistry.Register("monitor.*", ...) Line to
// cmd/thrum/main.go. DO NOT fix the test — fix the registration.
// Move the offending line to the server.RegisterHandler(...) Block that
// follows the unix-socket server construction.
func TestMonitorTrustBoundary_NotOnWebSocket(t *testing.T) {
	t.Run("runtime_registry_check", func(t *testing.T) {
		registry := websocket.NewSimpleRegistry()

		// Register a representative subset of the real wsRegistry methods so
		// that the enumeration path is exercised (not just an empty-registry
		// trivial pass).
		legitimateMethods := []string{
			"health",
			"agent.register",
			"agent.list",
			"session.start",
			"session.end",
			"group.create",
			"message.send",
			"message.list",
		}
		dummyHandler := websocket.Handler(func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, nil
		})
		for _, m := range legitimateMethods {
			registry.Register(m, dummyHandler)
		}

		registered := registry.RegisteredMethods()
		if len(registered) == 0 {
			t.Fatal("RegisteredMethods returned empty list; enumeration mechanism is broken")
		}

		for _, method := range registered {
			if strings.HasPrefix(method, "monitor.") {
				t.Errorf(
					"SECURITY: method %q is registered on the WebSocket transport; "+
						"monitor.* methods must only be on the unix socket. "+
						"See dev-docs/specs/2026-04-11-monitor-jobs-design.md § Trust boundary",
					method,
				)
			}
		}
	})

	t.Run("source_scan_main_go", func(t *testing.T) {
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
			// Detect wsRegistry.Register("monitor. lines.
			// We look for the prefix `wsRegistry.Register(` followed by a
			// string argument that begins with `"monitor.`.
			if strings.Contains(line, "wsRegistry.Register(") &&
				strings.Contains(line, `"monitor.`) {
				t.Errorf(
					"SECURITY: cmd/thrum/main.go:%d registers a monitor.* method on wsRegistry:\n\t%s\n"+
						"monitor.* methods must only be on the unix-socket server object.\n"+
						"Move this line to the server.RegisterHandler(...) block.",
					lineNum, strings.TrimSpace(line),
				)
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("error scanning main.go: %v", err)
		}
	})
}

// repoRootRelative walks up from this test file's directory until it finds a
// go.mod, then returns the path to the given relative target inside the repo.
func repoRootRelative(t *testing.T, relPath string) string {
	t.Helper()

	// Use the source file location baked in by the compiler at test time,
	// not os.Getwd(), so the path is correct regardless of where `go test`
	// is invoked from.
	_, thisFile, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate repo root")
	}

	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, relPath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}
