package backup

import (
	"context"
	"log"
	"time"
)

// BackupScheduler runs periodic backups on a configurable interval.
// It follows the same pattern as daemon.PeriodicSyncScheduler.
type BackupScheduler struct {
	interval  time.Duration
	buildOpts func() BackupOptions
}

// NewBackupScheduler creates a scheduler that calls RunBackup at the given interval.
// BuildOpts is called on each tick to construct fresh BackupOptions (allows
// dynamic config reload in the future).
func NewBackupScheduler(interval time.Duration, buildOpts func() BackupOptions) *BackupScheduler {
	return &BackupScheduler{
		interval:  interval,
		buildOpts: buildOpts,
	}
}

// Start begins the periodic backup loop. It blocks until ctx is canceled.
// The first backup runs after one full interval (not immediately on start).
func (s *BackupScheduler) Start(ctx context.Context) {
	log.Printf("backup_scheduler: starting with interval=%s", s.interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("backup_scheduler: stopping")
			return
		case <-ticker.C:
			s.runBackup()
		}
	}
}

// runBackup executes a single backup and logs the outcome.
func (s *BackupScheduler) runBackup() {
	opts := s.buildOpts()
	result, err := RunBackup(opts)
	if err != nil {
		log.Printf("backup_scheduler: backup failed: %v", err)
		return
	}
	log.Printf("backup_scheduler: backup completed — events=%d, messages=%d, tables=%d",
		result.SyncResult.EventLines, result.SyncResult.MessageFiles, len(result.LocalResult.Tables))
}
