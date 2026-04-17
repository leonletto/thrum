package permission

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestResolveSupervisorID_ProjectNameWins(t *testing.T) {
	cfg := &config.ThrumConfig{ProjectName: "Acme Co"}
	// Any repo path; won't be consulted because ProjectName is set.
	got := ResolveSupervisorID(cfg, "/tmp/irrelevant")
	// SanitizeAgentName lowercases and maps non-[a-z0-9_-] to "_", so
	// "Acme Co" → "acme_co". User slug is machine-dependent but must
	// follow after the sanitized repo slug.
	if !strings.HasPrefix(got, "supervisor_acme_co_") {
		t.Fatalf("got %q, want prefix supervisor_acme_co_", got)
	}
}

func TestResolveSupervisorID_FallsBackToBasename(t *testing.T) {
	cfg := &config.ThrumConfig{ProjectName: ""}
	tmp := t.TempDir()
	// t.TempDir() is not a git repo and has no origin — the basename
	// fallback fires.
	got := ResolveSupervisorID(cfg, tmp)
	want := filepath.Base(tmp)
	if !strings.HasPrefix(got, "supervisor_"+want+"_") {
		t.Fatalf("got %q, want prefix supervisor_%s_", got, want)
	}
}
