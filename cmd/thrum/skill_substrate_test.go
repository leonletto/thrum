package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/permission"
	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/skills"
)

// TestBuildSkillSubstrate_WiresAllCollaboratorsNonNil pins the
// brainstormer-third BLOCKING fix: the C-B1 substrate must be live in
// production, not dead code. The substrate has 3 collaborators
// (Worker, Watcher, Staleness); all must be non-nil after a successful
// build call. If a future refactor leaves one nil, this test catches
// it before the wiring drift escapes the binary.
func TestBuildSkillSubstrate_WiresAllCollaboratorsNonNil(t *testing.T) {
	t.Parallel()

	repoPath := t.TempDir()
	thrumDir := filepath.Join(repoPath, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "skills"), 0o750); err != nil {
		t.Fatalf("mkdir thrum/skills: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(thrumDir, "agents"), 0o750); err != nil {
		t.Fatalf("mkdir thrum/agents: %v", err)
	}
	// Initialize a git repo so safecmd.WorktreePaths has something to
	// walk; if git is unavailable the substrate-builder falls back to
	// [repoPath] which is fine for the wiring test (we're not asserting
	// destinations, just non-nil collaborators).
	_, _ = safecmd.Git(context.Background(), repoPath, "init", "--quiet")

	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "wiring.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	st, err := state.NewState(thrumDir, "", "test-repo", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	permPkg := permission.New(st, st.RawDB(), "supervisor_test", "test", thrumDir)
	store := reminders.NewSQLStore(safedb.New(st.RawDB()))
	library := skills.NewLibrary(repoPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	substrate, err := buildSkillSubstrate(ctx, skillSubstrateOpts{
		RepoPath:       repoPath,
		ThrumDir:       thrumDir,
		Library:        library,
		Permission:     permPkg,
		RemindersStore: store,
		DB:             st.DB(),
		PendingAfter:   48 * time.Hour,
	})
	if err != nil {
		t.Fatalf("buildSkillSubstrate: %v", err)
	}
	t.Cleanup(func() {
		_ = substrate.Watcher.Stop()
		_ = substrate.Worker.Stop()
	})

	if substrate.Worker == nil {
		t.Error("Worker is nil — mirror substrate would be dead code in production")
	}
	if substrate.Watcher == nil {
		t.Error("Watcher is nil — fsnotify-driven propose/promote/cancel flow inert")
	}
	if substrate.Staleness == nil {
		t.Error("Staleness is nil — proposal reminders never minted")
	}
}

// TestBuildSkillSubstrate_RejectsNilCollaborators pins the constructor's
// fail-fast invariant: every required field is checked and a missing
// one returns a descriptive error rather than constructing a half-wired
// substrate that surfaces the gap inside a goroutine 500ms later.
func TestBuildSkillSubstrate_RejectsNilCollaborators(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// Build the minimum-required dep set; we'll zero out one field per
	// sub-test and assert each fires its own descriptive error.
	repoPath := t.TempDir()
	thrumDir := filepath.Join(repoPath, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "skills"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "reject.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	st, _ := state.NewState(thrumDir, "", "test-repo", "")
	t.Cleanup(func() { _ = st.Close() })
	permPkg := permission.New(st, st.RawDB(), "supervisor_test", "test", thrumDir)
	store := reminders.NewSQLStore(safedb.New(st.RawDB()))
	library := skills.NewLibrary(repoPath)

	base := skillSubstrateOpts{
		RepoPath:       repoPath,
		ThrumDir:       thrumDir,
		Library:        library,
		Permission:     permPkg,
		RemindersStore: store,
		DB:             st.DB(),
		PendingAfter:   48 * time.Hour,
	}

	cases := []struct {
		name string
		opts skillSubstrateOpts
		want string
	}{
		{name: "nil library", opts: func() skillSubstrateOpts { o := base; o.Library = nil; return o }(), want: "Library"},
		{name: "nil permission", opts: func() skillSubstrateOpts { o := base; o.Permission = nil; return o }(), want: "Permission"},
		{name: "nil reminders store", opts: func() skillSubstrateOpts { o := base; o.RemindersStore = nil; return o }(), want: "RemindersStore"},
		{name: "nil db", opts: func() skillSubstrateOpts { o := base; o.DB = nil; return o }(), want: "DB"},
		{name: "empty repo path", opts: func() skillSubstrateOpts { o := base; o.RepoPath = ""; return o }(), want: "RepoPath"},
		{name: "empty thrum dir", opts: func() skillSubstrateOpts { o := base; o.ThrumDir = ""; return o }(), want: "ThrumDir"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := buildSkillSubstrate(ctx, c.opts)
			if err == nil {
				t.Fatalf("expected error mentioning %q; got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %q; want it to mention %q", err.Error(), c.want)
			}
		})
	}
}

// TestNewSkillChainResolver_ReturnsCoordinatorAgents pins the
// resolver-closure contract: queries agents WHERE role='coordinator'
// and returns the ID slice. The closure lives at the daemon-wiring
// layer (not in internal/skills/staleness.go) per plan v2 BLOCKING #3.
func TestNewSkillChainResolver_ReturnsCoordinatorAgents(t *testing.T) {
	t.Parallel()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "resolver.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	// Seed agents: 2 coordinators + 1 researcher.
	now := time.Now().UTC().Format(time.RFC3339)
	for _, row := range []struct{ id, role string }{
		{"@coord_alpha", "coordinator"},
		{"@coord_beta", "coordinator"},
		{"@researcher_x", "researcher"},
	} {
		if _, err := db.Exec(
			`INSERT INTO agents (agent_id, kind, role, module, display, registered_at, last_seen_at)
			 VALUES (?, 'agent', ?, 'test', ?, ?, ?)`,
			row.id, row.role, row.id, now, now,
		); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}

	resolver := newSkillChainResolver(safedb.New(db))
	chain, err := resolver(context.Background())
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("chain size = %d, want 2 coordinators; got %v", len(chain), chain)
	}
	got := map[string]struct{}{}
	for _, id := range chain {
		got[id] = struct{}{}
	}
	if _, ok := got["@coord_alpha"]; !ok {
		t.Errorf("missing @coord_alpha from chain: %v", chain)
	}
	if _, ok := got["@coord_beta"]; !ok {
		t.Errorf("missing @coord_beta from chain: %v", chain)
	}
	if _, ok := got["@researcher_x"]; ok {
		t.Errorf("researcher_x leaked into coordinator chain: %v", chain)
	}
}

// TestDestinationsForWorktrees_PairsWithKnownRuntimes confirms the
// cross-product behavior: every worktree path is paired with every
// known runtime. Adding a runtime to mirror.KnownRuntimes() (flipping
// its adapter table entry from nil) should require no daemon-boot
// wiring change.
func TestDestinationsForWorktrees_PairsWithKnownRuntimes(t *testing.T) {
	t.Parallel()
	wt1 := t.TempDir()
	wt2 := t.TempDir()
	dests := destinationsForWorktrees([]string{wt1, wt2})
	// 2 worktrees * len(KnownRuntimes) destinations.
	wantCount := 2 * 5 // claude + codex + opencode + kiro + cursor
	if len(dests) != wantCount {
		t.Fatalf("destinations = %d, want %d (2 worktrees * 5 known runtimes)", len(dests), wantCount)
	}
}

// TestDestinationsForWorktrees_SkipsMissingWorktrees confirms the
// stat-and-skip behavior: `git worktree list` can return paths whose
// checkouts were manually deleted; those should not enter the
// destinations slice (otherwise Worker.Start would error).
func TestDestinationsForWorktrees_SkipsMissingWorktrees(t *testing.T) {
	t.Parallel()
	wt1 := t.TempDir()
	missing := filepath.Join(t.TempDir(), "removed-by-rm")
	dests := destinationsForWorktrees([]string{wt1, missing})
	for _, d := range dests {
		if d.WorktreePath == missing {
			t.Errorf("destinations include missing worktree: %+v", d)
		}
	}
	if len(dests) == 0 {
		t.Error("destinations is empty; want entries for the one extant worktree")
	}
}

