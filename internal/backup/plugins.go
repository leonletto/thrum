package backup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

const pluginTimeout = 60 * time.Second

// PluginResult holds the outcome of a single plugin backup.
type PluginResult struct {
	Name      string
	Command   string
	Files     int
	CmdError  string // non-empty if command failed (non-fatal)
}

// RunPlugins executes backup plugins and collects their output files.
// Plugin failures are non-fatal: they are logged and the backup continues.
func RunPlugins(plugins []config.PluginConfig, repoPath, backupDir string) ([]PluginResult, error) {
	var results []PluginResult

	for _, p := range plugins {
		result := PluginResult{Name: p.Name, Command: p.Command}

		// Run plugin command
		if p.Command != "" {
			ctx, cancel := context.WithTimeout(context.Background(), pluginTimeout)
			cmd := exec.CommandContext(ctx, "sh", "-c", p.Command) //nolint:gosec // G204 - user-configured command
			cmd.Dir = repoPath
			cmd.Stdout = os.Stderr // route to stderr so it doesn't interfere with JSON output
			cmd.Stderr = os.Stderr

			if err := cmd.Run(); err != nil {
				result.CmdError = err.Error()
				cancel()
				results = append(results, result)
				continue // skip file collection if command failed
			}
			cancel()
		}

		// Collect files
		pluginBackupDir := filepath.Join(backupDir, "plugins", p.Name)
		for _, pattern := range p.Include {
			matches, err := filepath.Glob(filepath.Join(repoPath, pattern))
			if err != nil {
				continue
			}
			for _, match := range matches {
				relPath, err := filepath.Rel(repoPath, match)
				if err != nil {
					continue
				}
				dst := filepath.Join(pluginBackupDir, relPath)
				if _, err := atomicCopyFile(match, dst); err != nil {
					continue
				}
				result.Files++
			}
		}

		results = append(results, result)
	}

	return results, nil
}

// PluginNames returns the names of plugins that successfully ran.
func PluginNames(results []PluginResult) []string {
	var names []string
	for _, r := range results {
		if r.CmdError == "" {
			names = append(names, r.Name)
		}
	}
	return names
}

// PluginPresets are built-in plugin configurations for common tools.
var PluginPresets = map[string]config.PluginConfig{
	"beads": {
		Name:    "beads",
		Command: "bd backup --force",
		Include: []string{".beads/backup/*"},
	},
	"beads-rust": {
		Name:    "beads-rust",
		Command: "beads backup --force",
		Include: []string{".beads/backup/*"},
	},
}

// FormatPluginResults returns a human-readable summary of plugin results.
func FormatPluginResults(results []PluginResult) string {
	if len(results) == 0 {
		return ""
	}

	var b strings.Builder
	for _, r := range results {
		if r.CmdError != "" {
			fmt.Fprintf(&b, "  Plugin %s: FAILED (%s)\n", r.Name, r.CmdError)
		} else {
			fmt.Fprintf(&b, "  Plugin %s: %d files\n", r.Name, r.Files)
		}
	}
	return b.String()
}
