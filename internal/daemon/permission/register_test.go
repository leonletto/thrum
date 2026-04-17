package permission

import (
	"os"
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

func TestSupervisorIdentity_Shape(t *testing.T) {
	cfg := &config.ThrumConfig{ProjectName: "thrum"}
	idFile := SupervisorIdentity(cfg, "/Users/leon/dev/opensource/thrum")

	if !idFile.Reserved {
		t.Fatalf("Reserved=false, want true")
	}
	if idFile.Agent.Kind != "agent" {
		t.Fatalf("Kind=%q, want agent", idFile.Agent.Kind)
	}
	if idFile.Agent.Role != "supervisor" {
		t.Fatalf("Role=%q, want supervisor", idFile.Agent.Role)
	}
	if idFile.Agent.Module != "daemon" {
		t.Fatalf("Module=%q, want daemon", idFile.Agent.Module)
	}
	if !strings.HasPrefix(idFile.Agent.Name, "supervisor_thrum_") {
		t.Fatalf("Name=%q, want prefix supervisor_thrum_", idFile.Agent.Name)
	}
	if !strings.Contains(idFile.Agent.Display, "thrum") {
		t.Fatalf("Display=%q, want to contain 'thrum'", idFile.Agent.Display)
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

func TestCleanupLegacySupervisorFiles_RemovesSupervisorOnly(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Seed a supervisor file (should be removed).
	supervisorJSON := `{"version":5,"agent":{"Kind":"agent","Name":"supervisor_old","Role":"supervisor","Module":"daemon","Display":"Supervisor (old)"},"reserved":true}`
	supervisorPath := filepath.Join(identitiesDir, "supervisor_old.json")
	if err := os.WriteFile(supervisorPath, []byte(supervisorJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Seed a coordinator file (should NOT be removed).
	coordJSON := `{"version":5,"agent":{"Kind":"agent","Name":"coordinator_main","Role":"coordinator","Module":"main","Display":"Coordinator (main)"},"reserved":false}`
	coordPath := filepath.Join(identitiesDir, "coordinator_main.json")
	if err := os.WriteFile(coordPath, []byte(coordJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Seed a reserved-but-non-supervisor file (should NOT be removed —
	// this guards against over-aggressive cleanup if a future pseudo-
	// agent uses Reserved=true).
	otherReservedJSON := `{"version":5,"agent":{"Kind":"agent","Name":"other_reserved","Role":"system","Module":"daemon","Display":"Other"},"reserved":true}`
	otherReservedPath := filepath.Join(identitiesDir, "other_reserved.json")
	if err := os.WriteFile(otherReservedPath, []byte(otherReservedJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	CleanupLegacySupervisorFiles(thrumDir)

	if _, err := os.Stat(supervisorPath); !os.IsNotExist(err) {
		t.Fatalf("supervisor file not removed: stat err = %v", err)
	}
	if _, err := os.Stat(coordPath); err != nil {
		t.Fatalf("coordinator file should still exist: %v", err)
	}
	if _, err := os.Stat(otherReservedPath); err != nil {
		t.Fatalf("non-supervisor reserved file should still exist: %v", err)
	}
}

func TestCleanupLegacySupervisorFiles_NoIdentitiesDirIsNoop(t *testing.T) {
	tmp := t.TempDir()
	// Deliberately do not create .thrum/identities/.
	// Should not panic or error.
	CleanupLegacySupervisorFiles(tmp)
}
