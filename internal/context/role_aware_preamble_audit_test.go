package context

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAudit_RoleAwarePreambleCallSitesInAllowlist scans the repo for any .go
// files (excluding tests and the definition file) that call RoleAwarePreamble
// directly. Every match must be in a file listed in
// RoleAwarePreambleAllowedCallSites — otherwise it's a new bypass of the
// canonical RenderRoleTemplate path.
func TestAudit_RoleAwarePreambleCallSitesInAllowlist(t *testing.T) {
	repoRoot := findRepoRoot(t)
	allowed := make(map[string]bool, len(RoleAwarePreambleAllowedCallSites))
	for _, p := range RoleAwarePreambleAllowedCallSites {
		allowed[filepath.FromSlash(p)] = true
	}
	pat := regexp.MustCompile(`RoleAwarePreamble\s*\(`)

	var bypass []string
	walkErr := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := info.Name()
			if base == "node_modules" || base == "dist" || base == ".git" || base == "ui" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, path)
		if rel == filepath.FromSlash("internal/context/context.go") {
			return nil
		}
		body, readErr := os.ReadFile(path) // #nosec G304 -- path comes from filepath.Walk under repoRoot
		if readErr != nil {
			return nil
		}
		if pat.Match(body) {
			if !allowed[rel] {
				bypass = append(bypass, rel)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
	if len(bypass) > 0 {
		t.Errorf("RoleAwarePreamble called from non-allowlisted files (use RenderRoleTemplate first; add to RoleAwarePreambleAllowedCallSites if intentional):\n  %s", strings.Join(bypass, "\n  "))
	}
}

// findRepoRoot walks up from the test's CWD until it finds a directory
// containing go.mod. Used by the audit test so it works regardless of
// which subdirectory `go test` was invoked from.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("go.mod not found walking up from " + wd)
		}
		wd = parent
	}
}
