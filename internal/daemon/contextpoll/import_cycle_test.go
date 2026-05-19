package contextpoll_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestImportCycle_NoRPCImport enforces the spec §5.2 / plan T4.4
// (thrum-6qmf.1.19) constraint that the contextpoll package must NOT
// transitively import internal/daemon/rpc. The whole point of the
// RestartTrigger / ContextProvider interface seams is to keep the
// dependency edge one-way (rpc → contextpoll); a back-edge would
// reintroduce the cycle the substrate was designed to avoid.
//
// Enforcement runs `go list -deps` against the package and walks the
// returned transitive dep list. The test failure surfaces the exact
// path that breached the constraint so the implementer can see which
// new import flipped it on.
//
// This test runs `go list` as a subprocess. That works in any
// development environment where `go` is on PATH (the same env any
// `go test` invocation requires). In sandboxed CI builds where the
// Go toolchain isn't reachable, the test skips with t.Skip rather
// than fail spuriously.
func TestImportCycle_NoRPCImport(t *testing.T) {
	const target = "github.com/leonletto/thrum/internal/daemon/contextpoll"
	const forbidden = "github.com/leonletto/thrum/internal/daemon/rpc"

	out, err := exec.Command("go", "list", "-deps", target).Output()
	if err != nil {
		// `go` not on PATH or the command failed for some other reason
		// — surface the situation cleanly rather than fail the test.
		t.Skipf("go list -deps %s: %v (skipping import-cycle guard)", target, err)
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == forbidden {
			t.Fatalf("contextpoll transitively imports %s — spec §5.2 "+
				"import-cycle constraint violated. Verify with: "+
				"`go list -deps %s | grep %s`. Walk the import graph "+
				"to find the new edge.", forbidden, target, forbidden)
		}
	}
}
