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
// Someone added a wsRegistry.Register("message.deleteByAgent", ...) or
// wsRegistry.Register("message.deleteByScope", ...) line to cmd/thrum/main.go.
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
