package ingest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zora/internal/db"
	"zora/internal/jobs"
)

func TestScannerEnqueuesStableSupportedFilesAndDedupes(t *testing.T) {
	database := newIngestTestDB(t)
	root := t.TempDir()
	inbox := filepath.Join(root, "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(inbox, "bill.txt")
	writeOldFile(t, path, []byte("Payment due May 10."), now.Add(-time.Hour))

	scanner := Scanner{
		DB:             database,
		Jobs:           jobs.Store{DB: database},
		Inbox:          inbox,
		SettleDuration: 10 * time.Second,
		MaxAttempts:    3,
		Now:            func() time.Time { return now },
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Seen != 1 || result.Enqueued != 1 || result.Existing != 0 {
		t.Fatalf("first result = %+v", result)
	}

	result, err = scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if result.Enqueued != 0 || result.Existing != 1 {
		t.Fatalf("second result = %+v", result)
	}

	writeOldFile(t, path, []byte("Payment due June 10."), now.Add(-time.Hour))
	result, err = scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("changed Scan: %v", err)
	}
	if result.Enqueued != 1 {
		t.Fatalf("changed result = %+v", result)
	}
}

func TestScannerRecordsSettlingAndUnsupportedFiles(t *testing.T) {
	database := newIngestTestDB(t)
	root := t.TempDir()
	inbox := filepath.Join(root, "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	writeOldFile(t, filepath.Join(inbox, "fresh.txt"), []byte("still arriving"), now.Add(-5*time.Second))
	writeOldFile(t, filepath.Join(inbox, "photo.heic"), []byte("deferred format"), now.Add(-time.Hour))

	scanner := Scanner{
		DB:             database,
		Jobs:           jobs.Store{DB: database},
		Inbox:          inbox,
		SettleDuration: 10 * time.Second,
		MaxAttempts:    3,
		Now:            func() time.Time { return now },
	}

	result, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Settling != 1 || result.Ignored != 1 || result.Enqueued != 0 {
		t.Fatalf("result = %+v", result)
	}

	var ignored int
	if err := database.QueryRow(`SELECT COUNT(*) FROM watch_file_state WHERE ignored_reason IS NOT NULL`).Scan(&ignored); err != nil {
		t.Fatalf("count ignored states: %v", err)
	}
	if ignored != 2 {
		t.Fatalf("ignored states = %d", ignored)
	}
}

func newIngestTestDB(t *testing.T) *sql.DB {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "main.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(context.Background(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return database
}

func writeOldFile(t *testing.T, path string, data []byte, modTime time.Time) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}
