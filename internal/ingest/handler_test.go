package ingest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zora/internal/artifacts"
	"zora/internal/blobs"
	"zora/internal/extract"
	"zora/internal/jobs"
)

func TestFileHandlerStoresArtifactExtractsTextAndIndexesSearch(t *testing.T) {
	database := newIngestTestDB(t)
	root := t.TempDir()
	inbox := filepath.Join(root, "inbox")
	archive := filepath.Join(root, "archive")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	sourcePath := filepath.Join(inbox, "receipt.txt")
	writeOldFile(t, sourcePath, []byte("Receipt for payment to utility company."), now.Add(-time.Hour))

	contentHash, size, err := blobs.HashFile(sourcePath)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	absPath, err := filepath.Abs(sourcePath)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	sourceID := SourceID(absPath, contentHash)
	payload := FilePayload{
		Path:        absPath,
		ContentHash: contentHash,
		SourceID:    sourceID,
		SizeBytes:   size,
		MTime:       formatTime(now.Add(-time.Hour)),
		Type:        extract.TypeText,
		MIMEType:    "text/plain; charset=utf-8",
		Title:       "receipt",
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	handler := FileHandler{
		DB:        database,
		Blobs:     blobs.Store{ArchiveRoot: archive},
		Extractor: extract.LocalExtractor{Timeout: time.Minute},
		Owner:     "florian",
		Now:       func() time.Time { return now },
	}

	resultJSON, err := handler.HandleJob(context.Background(), jobs.Job{PayloadJSON: string(payloadJSON)})
	if err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	var result JobResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if result.ArtifactID != ArtifactID(sourceID) {
		t.Fatalf("artifact id = %q", result.ArtifactID)
	}

	var blobCount, artifactCount, textCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM blob WHERE hash = ?`, contentHash).Scan(&blobCount); err != nil {
		t.Fatalf("count blob: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM artifact WHERE id = ?`, result.ArtifactID).Scan(&artifactCount); err != nil {
		t.Fatalf("count artifact: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM extracted_text WHERE artifact_id = ?`, result.ArtifactID).Scan(&textCount); err != nil {
		t.Fatalf("count extracted_text: %v", err)
	}
	if blobCount != 1 || artifactCount != 1 || textCount != 1 {
		t.Fatalf("counts blob=%d artifact=%d text=%d", blobCount, artifactCount, textCount)
	}

	searchResults, err := artifacts.Search(context.Background(), database, "utility", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].ID != result.ArtifactID {
		t.Fatalf("search results = %+v", searchResults)
	}
}

func TestServiceScanAndWorkOneEndToEnd(t *testing.T) {
	database := newIngestTestDB(t)
	root := t.TempDir()
	inbox := filepath.Join(root, "inbox")
	archive := filepath.Join(root, "archive")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	writeOldFile(t, filepath.Join(inbox, "school-note.txt"), []byte("Please return the school form by Friday."), now.Add(-time.Hour))

	jobStore := jobs.Store{DB: database}
	service := Service{
		Scanner: Scanner{
			DB:             database,
			Jobs:           jobStore,
			Inbox:          inbox,
			SettleDuration: 10 * time.Second,
			MaxAttempts:    3,
			Now:            func() time.Time { return now },
		},
		Jobs: jobStore,
		Handler: FileHandler{
			DB:        database,
			Blobs:     blobs.Store{ArchiveRoot: archive},
			Extractor: extract.LocalExtractor{Timeout: time.Minute},
			Owner:     "florian",
			Now:       func() time.Time { return now },
		},
	}

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Enqueued != 1 {
		t.Fatalf("scan result = %+v", result)
	}
	worked, err := service.WorkOne(context.Background(), "test-worker")
	if err != nil {
		t.Fatalf("WorkOne: %v", err)
	}
	if !worked {
		t.Fatal("WorkOne did not claim a job")
	}

	counts, err := jobStore.CountsByStatus(context.Background())
	if err != nil {
		t.Fatalf("CountsByStatus: %v", err)
	}
	if counts[jobs.StatusSucceeded] != 1 {
		t.Fatalf("succeeded jobs = %d", counts[jobs.StatusSucceeded])
	}

	searchResults, err := artifacts.Search(context.Background(), database, "school", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(searchResults) != 1 {
		t.Fatalf("search results = %+v", searchResults)
	}
}
