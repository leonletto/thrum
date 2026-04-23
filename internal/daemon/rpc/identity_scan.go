package rpc

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// AllIdentityDirs returns every .thrum/identities/ directory across the
// primary thrumDir and all git worktrees discovered via
// `git worktree list --porcelain`.
//
// The primary directory is always the first entry. Worktree directories
// are added in git's reported order. Duplicates (primary appearing also
// as a worktree) are filtered out.
func AllIdentityDirs(ctx context.Context, thrumDir string) []string {
	var dirs []string
	primary := filepath.Join(thrumDir, "identities")
	dirs = append(dirs, primary)

	// Derive repo dir from thrumDir (thrumDir is typically "<repo>/.thrum")
	repoDir := filepath.Dir(thrumDir)

	for _, wtPath := range safecmd.WorktreePaths(ctx, repoDir) {
		idDir := filepath.Join(wtPath, ".thrum", "identities")
		if idDir == primary {
			continue // skip duplicate of primary
		}
		dirs = append(dirs, idDir)
	}
	return dirs
}

// IdentityPathsAcrossWorktrees returns the path selected for each agent by
// the same resolution algorithm as ReadIdentitiesAcrossWorktrees. Callers
// that need both the parsed file and its on-disk location should call this
// alongside ReadIdentitiesAcrossWorktrees (or switch to the combined
// form below if added). Added for thrum-51cg Option B: team.list
// enrichment needs the identity file path to clear stale TmuxSession
// values when the session is dead.
func IdentityPathsAcrossWorktrees(ctx context.Context, thrumDir string) map[string]string {
	_, paths := readIdentitiesAndPaths(ctx, thrumDir)
	return paths
}

// ReadIdentitiesAcrossWorktrees loads every identity file found under
// every dir returned by AllIdentityDirs and returns them keyed by agent
// name. When the same agent name appears in multiple dirs (cross-worktree
// drift), the file with the most recent UpdatedAt wins and a warning is
// logged listing the skipped paths.
//
// Directories that don't exist or can't be read are silently skipped —
// worktrees may be missing .thrum/identities/ without it being an error.
func ReadIdentitiesAcrossWorktrees(ctx context.Context, thrumDir string) map[string]*config.IdentityFile {
	files, _ := readIdentitiesAndPaths(ctx, thrumDir)
	return files
}

func readIdentitiesAndPaths(ctx context.Context, thrumDir string) (map[string]*config.IdentityFile, map[string]string) {
	files := make(map[string]*config.IdentityFile)
	paths := make(map[string]string)
	type entry struct {
		path string
		file *config.IdentityFile
	}
	seen := make(map[string][]entry)

	for _, dir := range AllIdentityDirs(ctx, thrumDir) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, de := range entries {
			if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, de.Name())
			data, err := os.ReadFile(path) // #nosec G304 -- internal identity file under known thrumDir
			if err != nil {
				continue
			}
			var idFile config.IdentityFile
			if err := json.Unmarshal(data, &idFile); err != nil {
				continue
			}
			name := idFile.Agent.Name
			if name == "" {
				// Fallback: derive from filename
				base := filepath.Base(path)
				name = base[:len(base)-len(filepath.Ext(base))]
			}
			seen[name] = append(seen[name], entry{path: path, file: &idFile})
		}
	}

	for name, entries := range seen {
		if len(entries) == 1 {
			files[name] = entries[0].file
			paths[name] = entries[0].path
			continue
		}
		// Pick the entry with the most recent UpdatedAt.
		best := entries[0]
		for _, e := range entries[1:] {
			if e.file.UpdatedAt.After(best.file.UpdatedAt) {
				best = e
			}
		}
		var skipped []string
		for _, e := range entries {
			if e.path != best.path {
				skipped = append(skipped, e.path)
			}
		}
		log.Printf("identity scan: divergent files for %q, using %s (skipped: %v)",
			name, best.path, skipped)
		files[name] = best.file
		paths[name] = best.path
	}

	return files, paths
}
