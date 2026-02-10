package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/leonletto/thrum/internal/paths"
)

// SetupOptions contains options for setting up a worktree redirect.
type SetupOptions struct {
	RepoPath string // Path to the worktree to set up
	MainRepo string // Path to the main repo (where daemon runs)
}

// Setup creates a .thrum/redirect in a worktree pointing to the main repo's .thrum/.
// This enables the worktree to share the daemon, database, and sync state.
func Setup(opts SetupOptions) error {
	// Resolve absolute paths
	repoPath, err := filepath.Abs(opts.RepoPath)
	if err != nil {
		return fmt.Errorf("resolve worktree path: %w", err)
	}

	mainAbsPath, err := filepath.Abs(opts.MainRepo)
	if err != nil {
		return fmt.Errorf("resolve main repo path: %w", err)
	}

	// Validate not setting up main repo as a redirect to itself
	if repoPath == mainAbsPath {
		return fmt.Errorf("cannot setup redirect to self — this is the main repo")
	}

	// Validate main repo is initialized
	mainThrumDir := filepath.Join(mainAbsPath, ".thrum")
	info, err := os.Stat(mainThrumDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("main repo not initialized — run 'thrum init' in the main repo first")
		}
		return fmt.Errorf("stat main repo .thrum/: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("main repo .thrum/ is not a directory")
	}

	// Create .thrum/ in worktree
	worktreeThrumDir := filepath.Join(repoPath, ".thrum")
	if err := os.MkdirAll(worktreeThrumDir, 0750); err != nil {
		return fmt.Errorf("create .thrum/ in worktree: %w", err)
	}

	// Write redirect file
	redirectPath := filepath.Join(worktreeThrumDir, "redirect")
	if err := os.WriteFile(redirectPath, []byte(mainThrumDir+"\n"), 0600); err != nil {
		return fmt.Errorf("write redirect file: %w", err)
	}

	// Create local identities directory (identities are per-worktree)
	identitiesDir := filepath.Join(worktreeThrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		return fmt.Errorf("create identities dir: %w", err)
	}

	// Verify redirect resolves correctly
	resolved, err := paths.ResolveThrumDir(repoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: redirect verification failed: %v\n", err)
	} else if resolved != mainThrumDir {
		fmt.Fprintf(os.Stderr, "Warning: redirect resolved to %s, expected %s\n", resolved, mainThrumDir)
	}

	// Check daemon reachability (optional, don't fail)
	socketPath := filepath.Join(mainThrumDir, "var", "thrum.sock")
	conn, err := net.Dial("unix", socketPath)
	if err == nil {
		_ = conn.Close()
		fmt.Println("Connected to daemon")
	} else {
		fmt.Println("Daemon not running — start it from the main repo")
	}

	return nil
}
