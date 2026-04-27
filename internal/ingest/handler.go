package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"zora/internal/blobs"
	"zora/internal/extract"
	"zora/internal/jobs"
)

type FileHandler struct {
	DB        *sql.DB
	Blobs     blobs.Store
	Extractor extract.Extractor
	Owner     string
	Now       func() time.Time
}

type JobResult struct {
	ArtifactID     string `json:"artifact_id"`
	ContentHash    string `json:"content_hash"`
	ExtractedChars int    `json:"extracted_chars"`
	Extractor      string `json:"extractor"`
}

func (h FileHandler) HandleJob(ctx context.Context, job jobs.Job) (string, error) {
	if h.DB == nil {
		return "", fmt.Errorf("ingest handler database is required")
	}
	if h.Extractor == nil {
		return "", fmt.Errorf("ingest handler extractor is required")
	}
	if h.Owner == "" {
		return "", fmt.Errorf("ingest handler owner is required")
	}

	var payload FilePayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return "", jobs.Permanent(fmt.Errorf("decode ingest payload: %w", err))
	}
	if payload.Path == "" || payload.ContentHash == "" || payload.SourceID == "" || payload.Type == "" {
		return "", jobs.Permanent(fmt.Errorf("ingest payload is missing required fields"))
	}

	object, err := h.Blobs.StoreFile(ctx, payload.Path)
	if err != nil {
		return "", err
	}
	if object.Hash != payload.ContentHash {
		return "", jobs.Permanent(fmt.Errorf("source file changed before ingest: expected sha256 %s, got %s", payload.ContentHash, object.Hash))
	}

	artifactID := ArtifactID(payload.SourceID)
	now := h.now()
	if err := h.persistArtifact(ctx, payload, object, artifactID, now); err != nil {
		return "", err
	}

	extracted, err := h.Extractor.Extract(ctx, object.StoragePath, payload.Type)
	if err != nil {
		if isPermanentExtractError(err) {
			return "", jobs.Permanent(err)
		}
		return "", err
	}

	if err := h.persistExtractedText(ctx, artifactID, payload.Title, extracted, now); err != nil {
		return "", err
	}

	result := JobResult{
		ArtifactID:     artifactID,
		ContentHash:    object.Hash,
		ExtractedChars: len(extracted.Text),
		Extractor:      extracted.Extractor,
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (h FileHandler) persistArtifact(ctx context.Context, payload FilePayload, object blobs.Object, artifactID string, now time.Time) error {
	metadata := map[string]any{
		"original_path": payload.Path,
		"size_bytes":    payload.SizeBytes,
		"mtime":         payload.MTime,
		"mime_type":     payload.MIMEType,
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin artifact transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO blob (hash, algorithm, size_bytes, mime_type, storage_path, created_at)
VALUES (?, ?, ?, NULLIF(?, ''), ?, ?)
ON CONFLICT(hash) DO UPDATE SET
	mime_type = COALESCE(excluded.mime_type, blob.mime_type),
	storage_path = excluded.storage_path
`,
		object.Hash,
		object.Algorithm,
		object.SizeBytes,
		payload.MIMEType,
		object.StoragePath,
		formatTime(now),
	); err != nil {
		return fmt.Errorf("upsert blob row: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO artifact (
	id, type, source, source_id, title, owner, content_hash,
	captured_at, event_at, metadata_json, created_at
) VALUES (?, ?, 'watch_folder', ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)
ON CONFLICT DO UPDATE SET
	title = excluded.title,
	content_hash = excluded.content_hash,
	event_at = excluded.event_at,
	metadata_json = excluded.metadata_json,
	deleted_at = NULL
`,
		artifactID,
		payload.Type,
		payload.SourceID,
		payload.Title,
		h.Owner,
		object.Hash,
		formatTime(now),
		payload.MTime,
		string(metadataJSON),
		formatTime(now),
	); err != nil {
		return fmt.Errorf("upsert artifact row: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit artifact transaction: %w", err)
	}
	return nil
}

func (h FileHandler) persistExtractedText(ctx context.Context, artifactID, title string, extracted extract.Result, now time.Time) error {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin extracted text transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO extracted_text (artifact_id, text, extractor, extractor_version, created_at)
VALUES (?, ?, ?, NULLIF(?, ''), ?)
ON CONFLICT(artifact_id) DO UPDATE SET
	text = excluded.text,
	extractor = excluded.extractor,
	extractor_version = excluded.extractor_version,
	created_at = excluded.created_at
`,
		artifactID,
		extracted.Text,
		extracted.Extractor,
		extracted.ExtractorVersion,
		formatTime(now),
	); err != nil {
		return fmt.Errorf("upsert extracted text: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO search_document (artifact_id, title, text, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(artifact_id) DO UPDATE SET
	title = excluded.title,
	text = excluded.text,
	updated_at = excluded.updated_at
`,
		artifactID,
		title,
		extracted.Text,
		formatTime(now),
	); err != nil {
		return fmt.Errorf("upsert search document: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit extracted text transaction: %w", err)
	}
	return nil
}

func (h FileHandler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func isPermanentExtractError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not installed or not on path") ||
		strings.Contains(message, "unsupported artifact type") ||
		strings.Contains(message, "not valid utf-8")
}
