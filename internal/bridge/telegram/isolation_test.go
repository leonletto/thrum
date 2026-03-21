package telegram

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBridgeImportIsolation enforces that the bridge package only imports
// internal/config from the thrum codebase. Any import of internal/daemon,
// internal/storage, or internal/websocket breaks the isolation boundary.
func TestBridgeImportIsolation(t *testing.T) {
	t.Parallel()

	forbidden := []string{
		"internal/daemon",
		"internal/storage",
		"internal/websocket",
	}

	// Find all .go files in the bridge package directory
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		path := filepath.Join(".", name)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			for _, fb := range forbidden {
				if strings.Contains(importPath, fb) {
					pos := fset.Position(imp.Pos())
					t.Errorf("ISOLATION VIOLATION: %s imports %q at %s",
						name, importPath, pos)
				}
			}
		}
	}
}

// TestBridgeOnlyPublicRPCs verifies that the bridge only calls allowed RPC methods.
func TestBridgeOnlyPublicRPCs(t *testing.T) {
	t.Parallel()

	allowedMethods := map[string]bool{
		"user.register":     true,
		"session.start":     true,
		"session.end":       true,
		"session.heartbeat": true,
		"message.send":      true,
		"message.get":       true,
		"message.markRead":  true,
	}

	// Parse all non-test Go files and find ws.Call / r.ws.Call invocations
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, name, nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Look for .Call(ctx, "method", ...)
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Call" {
				return true
			}

			if len(call.Args) < 2 {
				return true
			}

			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok {
				return true
			}

			method := strings.Trim(lit.Value, `"`)
			if !allowedMethods[method] {
				pos := fset.Position(call.Pos())
				t.Errorf("UNAUTHORIZED RPC: %s calls %q at %s",
					name, method, pos)
			}

			return true
		})
	}
}
