package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zora/internal/artifacts"
	"zora/internal/blobs"
	"zora/internal/extract"
	"zora/internal/jobs"
	"zora/internal/pipeline"
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
		DB:    database,
		Blobs: blobs.Store{ArchiveRoot: archive},
		Extractor: fakeExtractor{result: extract.Result{
			Text:           "Receipt for payment to utility company.",
			Markdown:       "# Receipt\n\nReceipt for payment to utility company.",
			StructuredJSON: `{"docling":true}`,
			Extractor:      "fake-docling",
			Status:         "success",
		}},
		Pipeline: &pipeline.Runner{DB: database, Now: func() time.Time { return now }},
		Owner:    "florian",
		Now:      func() time.Time { return now },
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

	var blobCount, artifactCount, textCount, documentCount, chunkCount, evidenceCount, classCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM blob WHERE hash = ?`, contentHash).Scan(&blobCount); err != nil {
		t.Fatalf("count blob: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM artifact WHERE id = ?`, result.ArtifactID).Scan(&artifactCount); err != nil {
		t.Fatalf("count artifact: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM extracted_text WHERE artifact_id = ?`, result.ArtifactID).Scan(&textCount); err != nil {
		t.Fatalf("count extracted_text: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM extracted_document WHERE artifact_id = ?`, result.ArtifactID).Scan(&documentCount); err != nil {
		t.Fatalf("count extracted_document: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM artifact_chunk WHERE artifact_id = ?`, result.ArtifactID).Scan(&chunkCount); err != nil {
		t.Fatalf("count artifact_chunk: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM evidence WHERE artifact_id = ?`, result.ArtifactID).Scan(&evidenceCount); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM artifact_classification WHERE artifact_id = ?`, result.ArtifactID).Scan(&classCount); err != nil {
		t.Fatalf("count artifact_classification: %v", err)
	}
	if blobCount != 1 || artifactCount != 1 || textCount != 1 || documentCount != 1 || chunkCount < 1 {
		t.Fatalf("counts blob=%d artifact=%d text=%d document=%d chunk=%d", blobCount, artifactCount, textCount, documentCount, chunkCount)
	}
	if evidenceCount < 1 || classCount != 1 {
		t.Fatalf("pipeline counts evidence=%d class=%d", evidenceCount, classCount)
	}
	if result.ChunkCount != chunkCount {
		t.Fatalf("result chunk count = %d, db chunk count = %d", result.ChunkCount, chunkCount)
	}

	searchResults, err := artifacts.Search(context.Background(), database, "utility", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].ID != result.ArtifactID {
		t.Fatalf("search results = %+v", searchResults)
	}
	var chunkID string
	if err := database.QueryRow(`
SELECT c.id
FROM artifact_chunk_fts
JOIN artifact_chunk c ON c.rowid = artifact_chunk_fts.rowid
WHERE artifact_chunk_fts MATCH 'utility'
`).Scan(&chunkID); err != nil {
		t.Fatalf("query chunk FTS: %v", err)
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

func TestServiceScanAndWorkOneWithDoclingPDF(t *testing.T) {
	database := newIngestTestDB(t)
	root := t.TempDir()
	inbox := filepath.Join(root, "inbox")
	archive := filepath.Join(root, "archive")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	docling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/convert/file" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"document": map[string]any{
				"md_content":   "# PDF Note\n\nPlease pay the invoice.",
				"text_content": "PDF Note\nPlease pay the invoice.",
				"json_content": map[string]any{"kind": "docling"},
			},
			"status":          "success",
			"processing_time": 0.25,
			"errors":          []any{},
		})
	}))
	defer docling.Close()

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	writeOldFile(t, filepath.Join(inbox, "invoice.pdf"), []byte("%PDF fake"), now.Add(-time.Hour))

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
			DB:    database,
			Blobs: blobs.Store{ArchiveRoot: archive},
			Extractor: extract.Router{
				Text: extract.LocalExtractor{Timeout: time.Minute},
				Binary: extract.DoclingExtractor{
					BaseURL:       docling.URL,
					OutputFormats: []string{"md", "text", "json"},
					DoOCR:         true,
					Timeout:       time.Minute,
				},
			},
			Owner: "florian",
			Now:   func() time.Time { return now },
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

	searchResults, err := artifacts.Search(context.Background(), database, "invoice", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(searchResults) != 1 {
		t.Fatalf("search results = %+v", searchResults)
	}
	var markdown string
	if err := database.QueryRow(`SELECT markdown FROM extracted_document`).Scan(&markdown); err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if markdown != "# PDF Note\n\nPlease pay the invoice." {
		t.Fatalf("markdown = %q", markdown)
	}
}

type fakeExtractor struct {
	result extract.Result
	err    error
}

func (f fakeExtractor) Extract(ctx context.Context, path string, artifactType string) (extract.Result, error) {
	return f.result, f.err
}
