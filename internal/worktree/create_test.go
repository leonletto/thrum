package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDerivePathAndBranch(t *testing.T) {
	cases := []struct {
		name           string
		opts           CreateOpts
		wantPathSuffix string // path relative to BasePath
		wantBranch     string
	}{
		{
			name: "ephemeral basic",
			opts: CreateOpts{
				BasePath:      "/tmp/wt",
				AgentName:     "docs_bot",
				JobID:         "job_01HABCDE",
				WakeTimestamp: 1715731200,
				Persistent:    false,
			},
			wantPathSuffix: "docs_bot-job_01HABCDE-1715731200",
			wantBranch:     "agent/docs_bot/job-job_01HABCDE-1715731200",
		},
		{
			name: "persistent basic",
			opts: CreateOpts{
				BasePath:   "/tmp/wt",
				AgentName:  "docs_bot",
				Persistent: true,
			},
			wantPathSuffix: "docs_bot",
			wantBranch:     "agent/docs_bot",
		},
		{
			name: "ephemeral with BranchOverride",
			opts: CreateOpts{
				BasePath:       "/tmp/wt",
				AgentName:      "x",
				JobID:          "j",
				WakeTimestamp:  1,
				Persistent:     false,
				BranchOverride: "feature/x",
			},
			wantPathSuffix: "x-j-1",
			wantBranch:     "feature/x",
		},
		{
			name: "persistent with BranchOverride",
			opts: CreateOpts{
				BasePath:       "/tmp/wt",
				AgentName:      "x",
				Persistent:     true,
				BranchOverride: "feature/x",
			},
			wantPathSuffix: "x",
			wantBranch:     "feature/x",
		},
		{
			name: "persistent without BranchOverride uses agent/<name> default",
			opts: CreateOpts{
				BasePath:   "/tmp/wt",
				AgentName:  "docs_bot",
				Persistent: true,
				// BranchOverride intentionally empty — verifies the
				// default agent/<AgentName> convention fires (Leon
				// Q1 was about cobra-side default; this is the
				// headless API default).
			},
			wantPathSuffix: "docs_bot",
			wantBranch:     "agent/docs_bot",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPath, gotBranch := derivePathAndBranch(c.opts)
			wantPath := c.opts.BasePath + "/" + c.wantPathSuffix
			if gotPath != wantPath {
				t.Errorf("path: got %q, want %q", gotPath, wantPath)
			}
			if gotBranch != c.wantBranch {
				t.Errorf("branch: got %q, want %q", gotBranch, c.wantBranch)
			}
		})
	}
}

func TestValidateOpts(t *testing.T) {
	cases := []struct {
		name    string
		opts    CreateOpts
		wantErr error // nil for pass; ErrInvalidOpts for fail
	}{
		{
			name: "valid ephemeral",
			opts: CreateOpts{
				RepoPath: "/repo", BasePath: "/wt",
				AgentName: "docs_bot", JobID: "j01", WakeTimestamp: 1,
				Persistent: false,
			},
			wantErr: nil,
		},
		{
			name: "valid persistent",
			opts: CreateOpts{
				RepoPath: "/repo", BasePath: "/wt",
				AgentName:  "docs_bot",
				Persistent: true,
			},
			wantErr: nil,
		},
		{
			name:    "empty RepoPath",
			opts:    CreateOpts{AgentName: "x", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "empty AgentName",
			opts:    CreateOpts{RepoPath: "/r", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName with slash",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "x/y", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName with ..",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "..", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName UPPER (rejected by identity.ValidateAgentName)",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "DOCS_BOT", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName with bang (rejected by identity.ValidateAgentName)",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "name!", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "AgentName 'daemon' (reserved by identity.ValidateAgentName)",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "daemon", Persistent: true},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "ephemeral missing JobID",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "x", Persistent: false, WakeTimestamp: 1},
			wantErr: ErrInvalidOpts,
		},
		{
			name:    "ephemeral missing WakeTimestamp",
			opts:    CreateOpts{RepoPath: "/r", AgentName: "x", Persistent: false, JobID: "j"},
			wantErr: ErrInvalidOpts,
		},
		{
			name: "persistent ignores JobID/WakeTimestamp",
			opts: CreateOpts{
				RepoPath: "/r", BasePath: "/wt", AgentName: "x",
				Persistent: true,
				// JobID and WakeTimestamp are zero-valued; per
				// spec §3.4 validation must SKIP these fields
				// when Persistent == true.
			},
			wantErr: nil,
		},
		{
			name: "resulting path > 255 bytes (spec §3.4 cap)",
			opts: CreateOpts{
				RepoPath: "/r",
				// 256-char BasePath forces leaf computation past
				// the 255-byte filesystem cap; ErrInvalidOpts at
				// API entry per spec §3.4 path-length guard.
				BasePath:  strings.Repeat("a", 256),
				AgentName: "x", Persistent: true,
			},
			wantErr: ErrInvalidOpts,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateOpts(c.opts)
			if c.wantErr == nil && err != nil {
				t.Errorf("got err %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Errorf("got err %v, want errors.Is(%v) true", err, c.wantErr)
			}
		})
	}
}

// newTestRepo bootstraps a temporary git repo with a thrum init
// state suitable for worktree-add operations. Returns the repo
// path and the worktree-base path (also temp).
func newTestRepo(t *testing.T) (repoPath, basePath string) {
	t.Helper()
	repoPath = t.TempDir()
	basePath = t.TempDir()

	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = repoPath
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.email", "test@example.com")
	runCmd("git", "config", "user.name", "Test")
	// Initial commit so worktree add has a HEAD to branch from.
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"),
		[]byte("init\n"), 0600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runCmd("git", "add", "README.md")
	runCmd("git", "commit", "-m", "init")
	runCmd("git", "branch", "-M", "main")

	// Minimal .thrum/ dir so EnsureRedirects has a main-repo target.
	if err := os.MkdirAll(filepath.Join(repoPath, ".thrum"), 0750); err != nil {
		t.Fatalf("mkdir .thrum: %v", err)
	}
	return repoPath, basePath
}

func TestCreate_EphemeralHappyPath(t *testing.T) {
	repoPath, basePath := newTestRepo(t)

	ctx := context.Background()
	result, err := Create(ctx, CreateOpts{
		RepoPath:      repoPath,
		BasePath:      basePath,
		AgentName:     "docs_bot",
		JobID:         "job_01HABCDE",
		WakeTimestamp: 1715731200,
		BaseBranch:    "main",
		Persistent:    false,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Reused {
		t.Error("Reused: got true, want false")
	}
	wantPath := filepath.Join(basePath,
		"docs_bot-job_01HABCDE-1715731200")
	if result.Path != wantPath {
		t.Errorf("Path: got %q, want %q", result.Path, wantPath)
	}
	wantBranch := "agent/docs_bot/job-job_01HABCDE-1715731200"
	if result.Branch != wantBranch {
		t.Errorf("Branch: got %q, want %q", result.Branch, wantBranch)
	}
	// Worktree directory exists.
	if _, err := os.Stat(result.Path); err != nil {
		t.Errorf("worktree path: %v", err)
	}
	// .thrum/redirect inside the new worktree.
	redirect := filepath.Join(result.Path, ".thrum", "redirect")
	if _, err := os.Stat(redirect); err != nil {
		t.Errorf("redirect: %v", err)
	}
}

// TestCreate_BasePathInferredWhenEmpty covers spec §3.5 priority
// chain tier 3: opts.BasePath unset AND no config.Worktrees.BasePath
// → fall back to InferBasePath(RepoPath). Uses an env-stubbed
// $HOME for determinism.
func TestCreate_BasePathInferredWhenEmpty(t *testing.T) {
	repoPath, _ := newTestRepo(t)
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	// Linux/darwin honor $HOME for os.UserHomeDir. Test confirms
	// InferBasePath returns $HOME/.thrum/worktrees/<project>.

	ctx := context.Background()
	result, err := Create(ctx, CreateOpts{
		RepoPath:      repoPath,
		BasePath:      "", // intentionally empty
		AgentName:     "x",
		JobID:         "j",
		WakeTimestamp: 1,
		BaseBranch:    "main",
		Persistent:    false,
	})
	if err != nil {
		t.Fatalf("Create with empty BasePath: %v", err)
	}
	projectName := filepath.Base(repoPath)
	wantBase := filepath.Join(fakeHome, ".thrum", "worktrees", projectName)
	wantPath := filepath.Join(wantBase, "x-j-1")
	if result.Path != wantPath {
		t.Errorf("Path: got %q, want %q (BasePath should fall through to InferBasePath)",
			result.Path, wantPath)
	}
}

func TestCreate_PersistentReuse(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	// First call: fresh create.
	r1, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "docs_bot", Persistent: true,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if r1.Reused {
		t.Error("first call Reused: got true, want false")
	}

	// Second call: idempotent reuse with err == nil.
	r2, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "docs_bot", Persistent: true,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if !r2.Reused {
		t.Error("second call Reused: got false, want true")
	}
	if r2.Path != r1.Path || r2.Branch != r1.Branch {
		t.Errorf("reuse mismatch: r1={%s,%s} r2={%s,%s}",
			r1.Path, r1.Branch, r2.Path, r2.Branch)
	}
}

func TestCreate_PersistentBranchMismatch(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	// Create a persistent worktree, then `git switch` it to an
	// unexpected branch to simulate operator squatting.
	r, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "docs_bot", Persistent: true,
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}
	// Switch to a freshly-created different branch in the worktree.
	cmd := exec.Command("git", "switch", "-c", "operator-branch")
	cmd.Dir = r.Path
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git switch: %v\n%s", err, out)
	}

	// Second Create on the same agent should now error.
	_, err = Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "docs_bot", Persistent: true,
		BaseBranch: "main",
	})
	if err == nil {
		t.Fatal("Create with mismatched branch: got nil err, want ErrPersistentBranchMismatch")
	}
	if !errors.Is(err, ErrPersistentBranchMismatch) {
		t.Errorf("err = %v, want errors.Is(ErrPersistentBranchMismatch) true", err)
	}
}

func TestCreate_EphemeralPathExists(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	// Pre-create the expected ephemeral path so the next Create
	// hits the pre-existence check.
	preExist := filepath.Join(basePath, "x-j-1")
	if err := os.MkdirAll(preExist, 0750); err != nil {
		t.Fatalf("pre-create: %v", err)
	}

	_, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "x", JobID: "j", WakeTimestamp: 1,
		Persistent: false, BaseBranch: "main",
	})
	if err == nil {
		t.Fatal("Create with pre-existing ephemeral path: got nil, want ErrPathExists")
	}
	if !errors.Is(err, ErrPathExists) {
		t.Errorf("err = %v, want errors.Is(ErrPathExists) true", err)
	}
}

func TestCreate_BestEffortCleanupOnRedirectFailure(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	expectedPath := filepath.Join(basePath, "x-j-1")

	// Fixture: post git worktree add, create the worktree's
	// .thrum/ entry as a FILE so EnsureRedirects's MkdirAll
	// fails with "not a directory." The worktree dir itself
	// remains writable so cleanup's `git worktree remove
	// --force` succeeds.
	testInjectAfterAdd = func(worktreePath string) {
		thrumPath := filepath.Join(worktreePath, ".thrum")
		if err := os.WriteFile(thrumPath, []byte("blocker"), 0600); err != nil {
			t.Fatalf("inject blocker: %v", err)
		}
	}
	t.Cleanup(func() { testInjectAfterAdd = nil })

	_, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "x", JobID: "j", WakeTimestamp: 1,
		Persistent: false, BaseBranch: "main",
	})
	if err == nil {
		t.Fatal("Create with failing EnsureRedirects: got nil, want error")
	}
	if !strings.Contains(err.Error(), "ensure redirects") {
		t.Errorf("err = %v, want wrap with 'ensure redirects'", err)
	}

	// Assert cleanup ran successfully: worktree dir gone,
	// branch deleted. (Per spec §3.5 best-effort cleanup
	// contract — non-cancellation error MUST leave zero
	// residue.)
	if _, statErr := os.Stat(expectedPath); !os.IsNotExist(statErr) {
		t.Errorf("worktree path: got err=%v (still present), want IsNotExist", statErr)
	}
	out, gerr := exec.Command("git", "-C", repoPath,
		"branch", "--list", "agent/x/job-j-1").CombinedOutput()
	if gerr != nil {
		t.Fatalf("git branch --list: %v\n%s", gerr, out)
	}
	if len(strings.TrimSpace(string(out))) != 0 {
		t.Errorf("branch still present: %s", out)
	}
}

func TestCreate_CleanupFails_ResidueInError(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	expectedPath := filepath.Join(basePath, "x-j-1")
	expectedBranch := "agent/x/job-j-1"

	// Two-stage fixture:
	//   1. Force EnsureRedirects to fail via .thrum=file trick.
	//   2. After triggering that failure, the cleanup path tries
	//      `git worktree remove --force` against expectedPath.
	//      Make THAT fail by chmod'ing the parent BasePath to
	//      0500 (read+execute only) inside the same inject hook
	//      so the unlink syscall returns EACCES.
	testInjectAfterAdd = func(worktreePath string) {
		thrumPath := filepath.Join(worktreePath, ".thrum")
		if err := os.WriteFile(thrumPath, []byte("blocker"), 0600); err != nil {
			t.Fatalf("inject blocker: %v", err)
		}
		if err := os.Chmod(basePath, 0500); err != nil { //#nosec G302 -- intentionally read-only to force cleanup-fails test path
			t.Fatalf("chmod basePath: %v", err)
		}
	}
	t.Cleanup(func() {
		testInjectAfterAdd = nil
		// Restore parent perms so t.TempDir cleanup works.
		_ = os.Chmod(basePath, 0700) //#nosec G302 -- restoring test dir for t.TempDir cleanup
	})

	_, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "x", JobID: "j", WakeTimestamp: 1,
		Persistent: false, BaseBranch: "main",
	})
	if err == nil {
		t.Fatal("Create with cleanup-fails: got nil err, want wrapped error with residue info")
	}
	msg := err.Error()
	// Spec §3.5 contract: residue info MUST be embedded in the
	// returned error string. Both path and branch must appear.
	if !strings.Contains(msg, "residue") {
		t.Errorf("err = %q, missing 'residue' marker per §3.5 contract", msg)
	}
	if !strings.Contains(msg, expectedPath) {
		t.Errorf("err = %q, missing residue worktree path %q", msg, expectedPath)
	}
	if !strings.Contains(msg, expectedBranch) {
		t.Errorf("err = %q, missing residue branch %q", msg, expectedBranch)
	}
}

func TestCreate_CancelPostAddSkipsCleanup(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Hook fires the cancel AFTER git worktree add succeeds.
	// ctx.Err() check inside Create then short-circuits before
	// EnsureRedirects runs.
	testInjectAfterAdd = func(worktreePath string) {
		cancel()
	}
	t.Cleanup(func() { testInjectAfterAdd = nil })

	expectedPath := filepath.Join(basePath, "x-j-1")

	_, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "x", JobID: "j", WakeTimestamp: 1,
		Persistent: false, BaseBranch: "main",
	})
	if err == nil {
		t.Fatal("Create with canceled ctx: got nil err, want context.Canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(context.Canceled) true", err)
	}

	// Residue class #4: worktree dir AND branch BOTH still present.
	if _, statErr := os.Stat(expectedPath); statErr != nil {
		t.Errorf("worktree path: got err=%v, want present (cleanup must be skipped on cancel)",
			statErr)
	}
	out, gerr := exec.Command("git", "-C", repoPath, "branch", "--list", "agent/x/job-j-1").CombinedOutput()
	if gerr != nil {
		t.Fatalf("git branch --list: %v\n%s", gerr, out)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Errorf("branch absent: cleanup must be skipped on cancel; got empty branch list")
	}
}

// TestCreate_AttachesExistingBranch — thrum-suyb. When BranchOverride
// names a branch that already exists in the repo, Create must attach
// it to the new worktree (no -b, no baseBranch) rather than erroring
// with "branch already exists". Verifies the worktree's HEAD lands on
// the pre-existing branch and that a unique commit on that branch is
// preserved (history not silently rebased onto baseBranch).
func TestCreate_AttachesExistingBranch(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	existing := "feature/preexisting"
	runRepo := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	// Create the pre-existing branch with a distinguishing commit
	// that does NOT live on main; later we'll assert the attached
	// worktree sees it.
	runRepo("git", "branch", existing)
	runRepo("git", "switch", existing)
	if err := os.WriteFile(filepath.Join(repoPath, "marker.txt"),
		[]byte("preexisting-content\n"), 0600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	runRepo("git", "add", "marker.txt")
	runRepo("git", "commit", "-m", "preexisting branch marker")
	// Switch repo back to main so the worktree-add target branch is
	// not already checked out in the main repo.
	runRepo("git", "switch", "main")

	result, err := Create(ctx, CreateOpts{
		RepoPath:       repoPath,
		BasePath:       basePath,
		AgentName:      "docs_bot",
		Persistent:     true,
		BaseBranch:     "main",
		BranchOverride: existing,
	})
	if err != nil {
		t.Fatalf("Create with existing branch: %v", err)
	}
	if result.Reused {
		t.Errorf("Reused: got true, want false (new path + existing branch is not reuse)")
	}
	if result.Branch != existing {
		t.Errorf("Branch: got %q, want %q", result.Branch, existing)
	}

	// Worktree HEAD must be on the pre-existing branch.
	headOut, err := exec.Command("git", "-C", result.Path,
		"rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD in worktree: %v", err)
	}
	if got := strings.TrimSpace(string(headOut)); got != existing {
		t.Errorf("worktree HEAD: got %q, want %q", got, existing)
	}

	// The pre-existing commit (marker.txt) must be visible — proves
	// we attached the existing branch, not silently re-created it
	// off baseBranch.
	if _, statErr := os.Stat(filepath.Join(result.Path, "marker.txt")); statErr != nil {
		t.Errorf("marker.txt in attached worktree: %v (history lost — attach used wrong base)",
			statErr)
	}
}

// TestCreate_RevParseFailureSurfaced — thrum-suyb safety. When the
// rev-parse branch-existence probe fails for a reason OTHER than
// "ref not found" (e.g. RepoPath is not a git repository → git
// exits 128), Create must return the rev-parse error directly
// instead of silently falling through to `git worktree add -b`
// (which would emit a confusing secondary error). Pins the
// isRefNotFoundError exit-code semantics.
func TestCreate_RevParseFailureSurfaced(t *testing.T) {
	// Use a fresh non-git tempdir as RepoPath. validateOpts does
	// not check RepoPath, so Create gets through to the rev-parse
	// probe, which then exits 128 ("not a git repository").
	repoPath := t.TempDir()
	basePath := t.TempDir()
	ctx := context.Background()

	_, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "x", JobID: "j", WakeTimestamp: 1,
		Persistent: false, BaseBranch: "main",
	})
	if err == nil {
		t.Fatal("Create against non-git RepoPath: got nil, want error")
	}
	if !strings.Contains(err.Error(), "rev-parse") {
		t.Errorf("err = %q, want error mentioning rev-parse (not the fall-through worktree-add error)",
			err)
	}
}

// TestCreate_AttachedBranchSurvivesCleanup — thrum-suyb safety. When
// Create attaches an existing branch and a subsequent step fails
// (e.g. EnsureRedirects), cleanup must remove the new worktree dir
// but MUST NOT delete the pre-existing branch (its history belongs
// to the caller). Companion guard to TestCreate_BestEffortCleanupOnRedirectFailure,
// which exercises the created-branch path where deletion IS expected.
func TestCreate_AttachedBranchSurvivesCleanup(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	existing := "feature/keep-me"
	runRepo := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	runRepo("git", "branch", existing)

	expectedPath := filepath.Join(basePath, "docs_bot")

	// Fail EnsureRedirects so the cleanup path runs.
	testInjectAfterAdd = func(worktreePath string) {
		thrumPath := filepath.Join(worktreePath, ".thrum")
		if err := os.WriteFile(thrumPath, []byte("blocker"), 0600); err != nil {
			t.Fatalf("inject blocker: %v", err)
		}
	}
	t.Cleanup(func() { testInjectAfterAdd = nil })

	_, err := Create(ctx, CreateOpts{
		RepoPath:       repoPath,
		BasePath:       basePath,
		AgentName:      "docs_bot",
		Persistent:     true,
		BaseBranch:     "main",
		BranchOverride: existing,
	})
	if err == nil {
		t.Fatal("Create with failing EnsureRedirects: got nil, want error")
	}

	// Worktree dir must be gone (cleanup ran).
	if _, statErr := os.Stat(expectedPath); !os.IsNotExist(statErr) {
		t.Errorf("worktree path: got err=%v (still present), want IsNotExist", statErr)
	}
	// Pre-existing branch must still exist (cleanup must NOT have
	// deleted it — that would lose the caller's history).
	out, gerr := exec.Command("git", "-C", repoPath,
		"branch", "--list", existing).CombinedOutput()
	if gerr != nil {
		t.Fatalf("git branch --list: %v\n%s", gerr, out)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Errorf("pre-existing branch %q deleted by cleanup — must be preserved (thrum-suyb)",
			existing)
	}
}
