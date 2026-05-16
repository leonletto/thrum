package scheduler

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

// WatchConfig starts a config-file watcher and registers a SIGHUP fallback.
// Calls onReload once per detected change. Stops when ctx is cancelled.
//
// Two paths feed onReload:
//  1. fsnotify on the file's PARENT directory — fsnotify on Darwin
//     reports RENAME events against the parent inode (not the renamed
//     file), so watching the file directly would miss the
//     write-tmp-and-rename pattern operators typically use to edit
//     configs atomically. Filter events to the configured filename.
//  2. SIGHUP — process-wide signal that lets operators force a reload
//     when fsnotify misses the change (canonical macOS rename-gap per
//     brainstorm Q8.3 + MINOR-18).
//
// Returns an error if the watcher could not be created or the directory
// could not be added. Otherwise spawns a goroutine that runs until ctx
// cancels.
func (s *Scheduler) WatchConfig(ctx context.Context, configPath string, onReload func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	dir := filepath.Dir(configPath)
	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)

	absConfig, _ := filepath.Abs(configPath)

	go func() {
		defer func() {
			_ = watcher.Close()
			signal.Stop(sigCh)
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				evAbs, _ := filepath.Abs(ev.Name)
				if evAbs != absConfig {
					continue
				}
				// WRITE catches in-place modifies; CREATE catches the
				// rename-and-replace second leg; RENAME catches the
				// first leg of write-tmp-and-rename. Any of the three
				// is a reload trigger.
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
					onReload()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				// Ignore closed-watcher errors during shutdown; log others.
				if !errors.Is(err, fsnotify.ErrEventOverflow) {
					log.Printf("scheduler: fsnotify error on %s: %v", configPath, err)
				}
			case <-sigCh:
				onReload()
			}
		}
	}()
	return nil
}
