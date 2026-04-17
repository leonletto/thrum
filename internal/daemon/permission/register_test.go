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

func TestResolveLegacySupervisorID_MatchesOldBinaryFallbacks(t *testing.T) {
	cases := []struct {
		name     string
		cfg      *config.ThrumConfig
		repoPath string
		want     string
	}{
		{
			name:     "ProjectName wins",
			cfg:      &config.ThrumConfig{ProjectName: "ThrumRepo"},
			repoPath: "/tmp/anywhere",
			want:     "supervisor_ThrumRepo",
		},
		{
			name:     "Falls back to basename",
			cfg:      &config.ThrumConfig{},
			repoPath: "/Users/leon/dev/opensource/thrum",
			want:     "supervisor_thrum",
		},
		{
			name:     "Falls back to 'project' when basename is unusable",
			cfg:      &config.ThrumConfig{},
			repoPath: "/",
			want:     "supervisor_project",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveLegacySupervisorID(tc.cfg, tc.repoPath)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
