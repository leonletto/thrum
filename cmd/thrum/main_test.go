package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/worktree"
)

func TestPrintAgentSummaryField(t *testing.T) {
	s := &cli.AgentSummary{
		AgentID:     "bob",
		Role:        "impl",
		TmuxAlive:   true,
		PID:         9001,
		TmuxSession: "bob:0.0",
		Host:        "laptop.local",
	}
	cases := []struct {
		field, want string
	}{
		{"agent_id", "bob\n"},
		{"role", "impl\n"},
		{"tmux_alive", "true\n"},
		{"pid", "9001\n"},
		{"tmux_session", "bob:0.0\n"},
		{"host", "laptop.local\n"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := printAgentSummaryField(&buf, s, tc.field); err != nil {
			t.Fatalf("field %q: %v", tc.field, err)
		}
		if buf.String() != tc.want {
			t.Fatalf("field %q: got %q, want %q", tc.field, buf.String(), tc.want)
		}
	}

	var buf bytes.Buffer
	err := printAgentSummaryField(&buf, s, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error message should mention 'unknown field': got %q", err.Error())
	}
}

func TestInferWorktreeBasePath_DefaultsToThrumWorktrees(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := worktree.InferBasePath("/some/path/falcon-backend")
	want := filepath.Join(home, ".thrum", "worktrees", "falcon-backend")
	if got != want {
		t.Errorf("worktree.InferBasePath = %q, want %q", got, want)
	}
}

// newTempRepoForCobraTest mirrors internal/worktree.newTestRepo
// but lives in package main since this test exercises the cobra
// command end-to-end. Pre-populates Worktrees.BasePath in the
// thrum config so the cobra wrapper's basePath resolution picks
// it up.
func newTempRepoForCobraTest(t *testing.T) (repoPath, basePath string) {
	t.Helper()
	repoPath = t.TempDir()
	basePath = t.TempDir()
	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = repoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.email", "test@example.com")
	runCmd("git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"),
		[]byte("init\n"), 0600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runCmd("git", "add", "README.md")
	runCmd("git", "commit", "-m", "init")
	runCmd("git", "branch", "-M", "main")
	if err := os.MkdirAll(filepath.Join(repoPath, ".thrum"), 0750); err != nil {
		t.Fatalf("mkdir .thrum: %v", err)
	}
	cfgPath := filepath.Join(repoPath, ".thrum", "config.json")
	cfgJSON := `{"worktrees":{"base_path":"` + basePath + `"}}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return repoPath, basePath
}

func TestWorktreeCreate_DefaultBranch(t *testing.T) {
	repoPath, basePath := newTempRepoForCobraTest(t)

	// Point flagRepo at the test repo. The existing pattern at
	// main_test.go:55 mutates the package-level flagRepo var
	// before invoking cmd.RunE; we mirror that here.
	flagRepo = repoPath
	t.Cleanup(func() { flagRepo = "" })

	// Ensure basePath includes the repo name suffix because the
	// cobra wrapper at line 2773 auto-appends it; mirror that
	// here so the assertion below matches the actual placement.
	repoName := filepath.Base(repoPath)
	if filepath.Base(basePath) != repoName {
		basePath = filepath.Join(basePath, repoName)
	}

	cmd := worktreeCreateCmd()
	cmd.SetArgs([]string{"smoke-test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("worktree create: %v", err)
	}

	// Worktree exists at base_path/<name>.
	wantPath := filepath.Join(basePath, "smoke-test")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("worktree path %q: %v", wantPath, err)
	}
	// Branch is feature/<name> (default per spec §4.1 + Leon Q1).
	out, err := exec.Command("git", "-C", repoPath,
		"branch", "--list", "feature/smoke-test").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Errorf("expected branch feature/smoke-test present, got empty")
	}
	// Redirect file points at the main repo's .thrum dir.
	redirect, err := os.ReadFile(filepath.Join(wantPath, ".thrum", "redirect"))
	if err != nil {
		t.Fatalf("read redirect: %v", err)
	}
	wantRedirect := filepath.Join(repoPath, ".thrum") + "\n"
	if string(redirect) != wantRedirect {
		t.Errorf("redirect: got %q, want %q", redirect, wantRedirect)
	}
	// Reference worktree package to confirm import is needed
	// regardless of assertion shape.
	_ = worktree.InferBasePath
}

func TestWorktreeTeardown_BranchStaysByDefault(t *testing.T) {
	repoPath, basePath := newTempRepoForCobraTest(t)
	flagRepo = repoPath
	t.Cleanup(func() { flagRepo = "" })

	// Mirror the cobra wrapper's repoName-suffix appending so the
	// assertion below targets the actual final path.
	repoName := filepath.Base(repoPath)
	if filepath.Base(basePath) != repoName {
		basePath = filepath.Join(basePath, repoName)
	}

	// Create a worktree first.
	createCmd := worktreeCreateCmd()
	createCmd.SetArgs([]string{"td-default"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Teardown without --delete-branch (default path).
	tdCmd := worktreeTeardownCmd()
	tdCmd.SetArgs([]string{"td-default"})
	if err := tdCmd.Execute(); err != nil {
		t.Fatalf("teardown: %v", err)
	}

	// Worktree gone.
	if _, err := os.Stat(filepath.Join(basePath, "td-default")); !os.IsNotExist(err) {
		t.Errorf("worktree path: got err=%v, want IsNotExist", err)
	}
	// Branch STILL present (pre-refactor parity per acceptance
	// criterion #10 default-path row).
	out, err := exec.Command("git", "-C", repoPath,
		"branch", "--list", "feature/td-default").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Errorf("branch absent after default teardown: want feature/td-default present")
	}
}

func TestWorktreeTeardown_DeleteBranchFlag(t *testing.T) {
	repoPath, basePath := newTempRepoForCobraTest(t)
	flagRepo = repoPath
	t.Cleanup(func() { flagRepo = "" })

	repoName := filepath.Base(repoPath)
	if filepath.Base(basePath) != repoName {
		basePath = filepath.Join(basePath, repoName)
	}

	createCmd := worktreeCreateCmd()
	createCmd.SetArgs([]string{"td-flag"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}

	tdCmd := worktreeTeardownCmd()
	tdCmd.SetArgs([]string{"td-flag", "--delete-branch"})
	if err := tdCmd.Execute(); err != nil {
		t.Fatalf("teardown --delete-branch: %v", err)
	}

	// Worktree gone.
	if _, err := os.Stat(filepath.Join(basePath, "td-flag")); !os.IsNotExist(err) {
		t.Errorf("worktree path: got err=%v, want IsNotExist", err)
	}
	// Branch GONE (Leon Q2 lock path).
	out, err := exec.Command("git", "-C", repoPath,
		"branch", "--list", "feature/td-flag").CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v\n%s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("branch still present after --delete-branch: %s", out)
	}
}
