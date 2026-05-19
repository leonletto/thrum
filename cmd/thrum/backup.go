package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/backup"
	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:8901-8952
// Destination: backup.go:24-75
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func backupCmd() *cobra.Command {
	var flagDir string

	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup thrum data",
		Long:  "Snapshot all thrum data (events, messages, config, identities) to a backup directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupCreate(flagDir)
		},
	}

	cmd.PersistentFlags().StringVar(&flagDir, "dir", "", "Override backup directory")

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show last backup info",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupStatus(flagDir)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "config",
		Short: "Show effective backup config",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupConfig()
		},
	})

	var flagYes bool
	restoreCmd := &cobra.Command{
		Use:   "restore [archive.zip]",
		Short: "Restore from backup",
		Long:  "Restore thrum data from the latest backup or a specific archive zip.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var archivePath string
			if len(args) > 0 {
				archivePath = args[0]
			}
			return runBackupRestore(flagDir, archivePath, flagYes)
		},
	}
	restoreCmd.Flags().BoolVar(&flagYes, "yes", false, "Skip confirmation prompt")
	cmd.AddCommand(restoreCmd)

	cmd.AddCommand(pluginCmd())
	cmd.AddCommand(scheduleCmd())

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:8954-8999
// Destination: backup.go:83-128
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func scheduleCmd() *cobra.Command {
	var flagScheduleDir string

	cmd := &cobra.Command{
		Use:   "schedule [interval|off]",
		Short: "Configure automatic backup schedule",
		Long: `View or set the automatic backup schedule. The daemon runs backups at the
configured interval when running.

Examples:
  thrum backup schedule            Show current schedule
  thrum backup schedule 24h        Back up every 24 hours
  thrum backup schedule 12h        Back up every 12 hours
  thrum backup schedule 6h         Back up every 6 hours
  thrum backup schedule 30m        Back up every 30 minutes
  thrum backup schedule off        Disable scheduled backups
  thrum backup schedule 24h --dir /path/to/backups

Intervals use Go duration format: "24h", "12h", "6h30m", "168h" (1 week).

The schedule is stored in .thrum/config.json under backup.schedule. The daemon
must be restarted for schedule changes to take effect.

Third-party backup plugins can be configured manually in .thrum/config.json:

  {
    "backup": {
      "schedule": "24h",
      "plugins": [
        {"name": "beads", "command": "bd backup --force", "include": [".beads/backup/*"]}
      ],
      "post_backup": "echo backup done"
    }
  }

Use 'thrum backup plugin add' to manage plugins via CLI.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupSchedule(args, flagScheduleDir)
		},
	}

	cmd.Flags().StringVar(&flagScheduleDir, "dir", "", "Set backup directory")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:9001-9070
// Destination: backup.go:136-205
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runBackupSchedule(args []string, dirOverride string) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Show mode: no args
	if len(args) == 0 {
		if cfg.Backup.Schedule == "" {
			fmt.Println("Backup schedule: disabled")
		} else {
			fmt.Printf("Backup schedule: every %s\n", cfg.Backup.Schedule)
		}
		backupDir := cfg.Backup.Dir
		if backupDir == "" {
			backupDir = filepath.Join(thrumDir, "backup")
		}
		fmt.Printf("Backup directory: %s\n", backupDir)

		// Show last backup time from manifest
		repoName := cli.GetRepoName(flagRepo)
		manifestPath := filepath.Join(backupDir, repoName, "current", "manifest.json")
		if data, readErr := os.ReadFile(filepath.Clean(manifestPath)); readErr == nil {
			var manifest map[string]any
			if json.Unmarshal(data, &manifest) == nil {
				if ts, ok := manifest["timestamp"].(string); ok {
					fmt.Printf("Last backup: %s\n", ts)
				}
			}
		}

		fmt.Println("\nRestart the daemon for schedule changes to take effect.")
		return nil
	}

	// Set mode
	interval := args[0]
	if interval == "off" || interval == "disable" || interval == "none" {
		cfg.Backup.Schedule = ""
		fmt.Println("Backup schedule: disabled")
	} else {
		// Validate it's a valid Go duration
		d, parseErr := time.ParseDuration(interval)
		if parseErr != nil {
			return fmt.Errorf("invalid interval %q: use Go duration format (e.g., 24h, 12h, 6h30m): %w", interval, parseErr)
		}
		if d <= 0 {
			return fmt.Errorf("interval must be positive, got %s", d)
		}
		cfg.Backup.Schedule = interval
		fmt.Printf("Backup schedule: every %s\n", interval)
	}

	if dirOverride != "" {
		cfg.Backup.Dir = dirOverride
		fmt.Printf("Backup directory: %s\n", dirOverride)
	}

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println("\nRestart the daemon for schedule changes to take effect.")
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:9072-9136
// Destination: backup.go:213-277
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runBackupCreate(dirOverride string) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Resolve backup dir: CLI flag > config > default
	backupDir := dirOverride
	if backupDir == "" {
		backupDir = cfg.Backup.Dir
	}
	if backupDir == "" {
		backupDir = filepath.Join(thrumDir, "backup")
	}

	// Resolve sync worktree
	syncDir, err := paths.SyncWorktreePath(flagRepo)
	if err != nil {
		syncDir = "" // non-fatal: sync dir may not exist yet
	}

	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	repoName := cli.GetRepoName(flagRepo)

	result, err := backup.RunBackup(backup.BackupOptions{
		BackupDir:    backupDir,
		RepoName:     repoName,
		SyncDir:      syncDir,
		ThrumDir:     thrumDir,
		DBPath:       dbPath,
		ThrumVersion: Version,
		Retention:    &cfg.Backup.Retention,
		Plugins:      cfg.Backup.Plugins,
		PostBackup:   cfg.Backup.PostBackup,
		RepoPath:     flagRepo,
	})
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	if flagJSON {
		return cli.EmitJSON(result.Manifest)
	}
	fmt.Printf("Backup complete: %s\n", result.CurrentDir)
	fmt.Printf("  Events: %d lines\n", result.SyncResult.EventLines)
	fmt.Printf("  Message files: %d\n", result.SyncResult.MessageFiles)
	fmt.Printf("  Local tables: %d\n", len(result.LocalResult.Tables))
	fmt.Printf("  Config files: %d\n", result.Manifest.Counts.ConfigFiles)
	if pluginSummary := backup.FormatPluginResults(result.PluginResults); pluginSummary != "" {
		fmt.Printf("  Plugins:\n%s", pluginSummary)
	}
	if result.PostHookResult != nil {
		if result.PostHookResult.Error != "" {
			fmt.Printf("  Post-backup hook: FAILED (%s)\n", result.PostHookResult.Error)
		} else {
			fmt.Printf("  Post-backup hook: ok\n")
		}
	}
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:9138-9215
// Destination: backup.go:285-362
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runBackupStatus(dirOverride string) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	backupDir := dirOverride
	if backupDir == "" {
		backupDir = cfg.Backup.Dir
	}
	if backupDir == "" {
		backupDir = filepath.Join(thrumDir, "backup")
	}

	repoName := cli.GetRepoName(flagRepo)
	currentDir := filepath.Join(backupDir, repoName, "current")

	manifest, err := backup.ReadManifest(currentDir)
	if err != nil {
		return fmt.Errorf("no backup found (looked in %s): %w", currentDir, err)
	}

	if flagJSON {
		return cli.EmitJSON(manifest)
	}
	fmt.Printf("Last backup: %s\n", manifest.Timestamp.Local().Format("2006-01-02 15:04:05"))
	fmt.Printf("  Thrum version: %s\n", manifest.ThrumVersion)
	fmt.Printf("  Repo: %s\n", manifest.RepoName)
	fmt.Printf("  Events: %d\n", manifest.Counts.Events)
	fmt.Printf("  Message files: %d\n", manifest.Counts.MessageFiles)
	fmt.Printf("  Local tables: %d\n", manifest.Counts.LocalTables)
	fmt.Printf("  Config files: %d\n", manifest.Counts.ConfigFiles)
	if len(manifest.Counts.Plugins) > 0 {
		fmt.Printf("  Plugins: %v\n", manifest.Counts.Plugins)
	}
	fmt.Printf("  Location: %s\n", currentDir)

	// Show archive rotation stats
	archivesDir := filepath.Join(backupDir, repoName, "archives")
	if entries, err := os.ReadDir(archivesDir); err == nil {
		var archiveCount int
		var totalSize int64
		var oldest, newest time.Time
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), "pre-restore-") {
				continue
			}
			archiveCount++
			if info, err := e.Info(); err == nil {
				totalSize += info.Size()
			}
			// Parse timestamp from filename (2006-01-02T150405.zip)
			name := strings.TrimSuffix(e.Name(), ".zip")
			if ts, err := time.Parse("2006-01-02T150405", name); err == nil {
				if oldest.IsZero() || ts.Before(oldest) {
					oldest = ts
				}
				if newest.IsZero() || ts.After(newest) {
					newest = ts
				}
			}
		}
		if archiveCount > 0 {
			fmt.Printf("Archives: %d (%.1f MB)\n", archiveCount, float64(totalSize)/(1024*1024))
			if !oldest.IsZero() {
				fmt.Printf("  Oldest: %s\n", oldest.Local().Format("2006-01-02 15:04:05"))
				fmt.Printf("  Newest: %s\n", newest.Local().Format("2006-01-02 15:04:05"))
			}
		}
	}

	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:9217-9255
// Destination: backup.go:370-408
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runBackupConfig() error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	effectiveDir := cfg.Backup.Dir
	if effectiveDir == "" {
		effectiveDir = filepath.Join(thrumDir, "backup") + " (default)"
	}

	if flagJSON {
		return cli.EmitJSON(cfg.Backup)
	}
	fmt.Printf("Backup directory: %s\n", effectiveDir)
	fmt.Printf("Retention:\n")
	fmt.Printf("  Daily: %d\n", cfg.Backup.Retention.RetentionDaily())
	fmt.Printf("  Weekly: %d\n", cfg.Backup.Retention.RetentionWeekly())
	monthly := fmt.Sprintf("%d", cfg.Backup.Retention.RetentionMonthly())
	if cfg.Backup.Retention.RetentionMonthly() == -1 {
		monthly = "forever"
	}
	fmt.Printf("  Monthly: %s\n", monthly)
	if len(cfg.Backup.Plugins) > 0 {
		fmt.Printf("Plugins:\n")
		for _, p := range cfg.Backup.Plugins {
			fmt.Printf("  %s: %s\n", p.Name, p.Command)
		}
	}
	if cfg.Backup.PostBackup != "" {
		fmt.Printf("Post-backup: %s\n", cfg.Backup.PostBackup)
	}
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:9257-9348
// Destination: backup.go:416-507
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runBackupRestore(dirOverride, archivePath string, skipConfirm bool) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	backupDir := dirOverride
	if backupDir == "" {
		backupDir = cfg.Backup.Dir
	}
	if backupDir == "" {
		backupDir = filepath.Join(thrumDir, "backup")
	}

	repoName := cli.GetRepoName(flagRepo)

	if !skipConfirm {
		fmt.Printf("This will restore thrum data from backup.\n")
		fmt.Printf("  Backup dir: %s\n", backupDir)
		fmt.Printf("  Repo: %s\n", repoName)
		if archivePath != "" {
			fmt.Printf("  Archive: %s\n", archivePath)
		} else {
			fmt.Printf("  Source: current/\n")
		}
		fmt.Printf("A safety backup will be created first.\n")
		fmt.Printf("Continue? [y/N] ")

		var answer string
		_, _ = fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" && answer != "yes" {
			fmt.Println("Restore canceled.")
			return nil
		}
	}

	// Stop daemon before restore to avoid file handle conflicts
	daemonWasRunning := false
	if stopErr := cli.DaemonStop(flagRepo); stopErr == nil {
		daemonWasRunning = true
		fmt.Println("Daemon stopped for restore.")
	}

	syncDir, err := paths.SyncWorktreePath(flagRepo)
	if err != nil {
		syncDir = ""
	}

	dbPath := filepath.Join(thrumDir, "var", "messages.db")

	result, err := backup.RunRestore(backup.RestoreOptions{
		BackupDir:   backupDir,
		RepoName:    repoName,
		ArchivePath: archivePath,
		SyncDir:     syncDir,
		ThrumDir:    thrumDir,
		DBPath:      dbPath,
		Plugins:     cfg.Backup.Plugins,
		RepoPath:    flagRepo,
	})
	if err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	if flagJSON {
		if err := cli.EmitJSON(result); err != nil {
			return err
		}
	} else {
		if result.SafetyBackup != "" {
			fmt.Printf("Safety backup: %s\n", result.SafetyBackup)
		}
		fmt.Printf("Restored from: %s\n", result.Source)
	}

	// Restart daemon if it was running before restore
	if daemonWasRunning {
		if restartErr := cli.DaemonRestart(flagRepo, cfg.Daemon.LocalOnly, false); restartErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not restart daemon: %v\n", restartErr)
			fmt.Println("Restart manually: thrum daemon start")
		} else {
			fmt.Println("Daemon restarted. SQLite will rebuild from restored JSONL.")
		}
	}

	return nil
}
