package backup

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

const archiveTimeFormat = "2006-01-02T150405"

// CompressCurrentToArchive compresses the current/ backup directory into a
// timestamped zip in archives/. Returns the path to the created zip.
func CompressCurrentToArchive(currentDir, archivesDir string) (string, error) {
	// Check if current dir exists and has content
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // nothing to archive
		}
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}

	if err := os.MkdirAll(archivesDir, 0750); err != nil {
		return "", fmt.Errorf("create archives dir: %w", err)
	}

	timestamp := time.Now().UTC().Format(archiveTimeFormat)
	zipPath := filepath.Join(archivesDir, timestamp+".zip")
	tmpPath := zipPath + ".tmp"

	if err := createZip(currentDir, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("create zip: %w", err)
	}

	if err := os.Rename(tmpPath, zipPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	// Clear current dir for the new backup
	if err := os.RemoveAll(currentDir); err != nil {
		return zipPath, fmt.Errorf("clear current dir: %w", err)
	}

	return zipPath, nil
}

// createZip compresses a directory into a zip file.
func createZip(srcDir, zipPath string) error {
	f, err := os.Create(zipPath) //nolint:gosec // G304 - internal path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := zip.NewWriter(f)
	defer func() { _ = w.Close() }()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zip.Deflate

		writer, err := w.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(path) //nolint:gosec // G304 - walking internal directory
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()

		_, err = io.Copy(writer, file)
		return err
	})
}

// ApplyRetention applies GFS (Grandfather-Father-Son) retention to archive files.
// Files matching "pre-restore-*.zip" are exempt from rotation.
func ApplyRetention(archivesDir string, retention config.RetentionConfig) error {
	entries, err := os.ReadDir(archivesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	type archive struct {
		path string
		time time.Time
	}

	var archives []archive
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		// Skip pre-restore safety backups
		if strings.HasPrefix(e.Name(), "pre-restore-") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".zip")
		t, err := time.Parse(archiveTimeFormat, name)
		if err != nil {
			continue // skip unrecognized files
		}
		archives = append(archives, archive{
			path: filepath.Join(archivesDir, e.Name()),
			time: t,
		})
	}

	if len(archives) == 0 {
		return nil
	}

	// Sort newest first
	sort.Slice(archives, func(i, j int) bool {
		return archives[i].time.After(archives[j].time)
	})

	// Classify and determine which to keep
	keep := make(map[string]bool)

	// Daily: keep the N most recent
	daily := retention.RetentionDaily()
	if daily > 0 {
		for i := range min(daily, len(archives)) {
			keep[archives[i].path] = true
		}
	}

	// Weekly: keep the oldest surviving archive per ISO week
	weekBuckets := make(map[string]*archive) // "YYYY-WW" → oldest
	for i := range archives {
		a := &archives[i]
		year, week := a.time.ISOWeek()
		key := fmt.Sprintf("%04d-%02d", year, week)
		if existing, ok := weekBuckets[key]; !ok || a.time.Before(existing.time) {
			weekBuckets[key] = a
		}
	}

	// Sort weeks newest first, keep N
	type weekEntry struct {
		key string
		a   *archive
	}
	var weeks []weekEntry
	for k, v := range weekBuckets {
		weeks = append(weeks, weekEntry{k, v})
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].key > weeks[j].key })

	weekly := retention.RetentionWeekly()
	if weekly > 0 {
		for i := range min(weekly, len(weeks)) {
			keep[weeks[i].a.path] = true
		}
	}

	// Monthly: keep the oldest surviving archive per month
	monthly := retention.RetentionMonthly()
	if monthly != 0 { // -1 means keep forever
		monthBuckets := make(map[string]*archive) // "YYYY-MM" → oldest
		for i := range archives {
			a := &archives[i]
			key := a.time.Format("2006-01")
			if existing, ok := monthBuckets[key]; !ok || a.time.Before(existing.time) {
				monthBuckets[key] = a
			}
		}

		type monthEntry struct {
			key string
			a   *archive
		}
		var months []monthEntry
		for k, v := range monthBuckets {
			months = append(months, monthEntry{k, v})
		}
		sort.Slice(months, func(i, j int) bool { return months[i].key > months[j].key })

		limit := len(months) // -1 = keep all
		if monthly > 0 {
			limit = min(monthly, len(months))
		}
		for i := range limit {
			keep[months[i].a.path] = true
		}
	}

	// Delete archives not in keep set
	for _, a := range archives {
		if !keep[a.path] {
			if err := os.Remove(a.path); err != nil {
				return fmt.Errorf("remove %s: %w", filepath.Base(a.path), err)
			}
		}
	}

	return nil
}
