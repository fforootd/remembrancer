package artifacts

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type Artifact struct {
	ID          string
	Type        string
	Title       string
	EventAt     sql.NullString
	CreatedAt   string
	HasText     bool
	TextPreview sql.NullString
	ChunkCount  int
}

type SearchResult struct {
	ID      string
	Type    string
	Title   string
	EventAt sql.NullString
	Snippet string
}

type ArtifactDetail struct {
	ID           string
	Type         string
	Title        string
	Source       string
	SourceID     sql.NullString
	Owner        string
	CapturedAt   string
	EventAt      sql.NullString
	CreatedAt    string
	MetadataJSON sql.NullString

	BlobHash        string
	BlobAlgorithm   string
	BlobSizeBytes   int64
	BlobMIMEType    sql.NullString
	BlobStoragePath string

	HasText          bool
	TextExtractor    sql.NullString
	TextExtractorVer sql.NullString
	TextChars        int
	Text             sql.NullString
	TextCreatedAt    sql.NullString

	HasDocument         bool
	DocStatus           sql.NullString
	DocExtractor        sql.NullString
	DocExtractorVer     sql.NullString
	DocProcessingTimeMS sql.NullInt64
	MarkdownChars       int
	Markdown            sql.NullString
	StructuredJSON      sql.NullString
	DocMetadataJSON     sql.NullString
	WarningsJSON        sql.NullString
	ErrorsJSON          sql.NullString
	DocCreatedAt        sql.NullString

	WatchPath         sql.NullString
	LastEnqueuedJobID sql.NullString

	Chunks []ChunkSummary
}

type ChunkSummary struct {
	ID           string
	Ordinal      int
	Title        sql.NullString
	HeadingPath  sql.NullString
	PageStart    sql.NullInt64
	PageEnd      sql.NullInt64
	CharStart    int
	CharEnd      int
	TextChars    int
	Text         string
	MetadataJSON sql.NullString
}

type BlobInfo struct {
	Hash        string
	Algorithm   string
	SizeBytes   int64
	MIMEType    sql.NullString
	StoragePath string
	Title       string
	Type        string
}

func GetBlob(ctx context.Context, db *sql.DB, artifactID string) (BlobInfo, bool, error) {
	row := db.QueryRowContext(ctx, `
SELECT b.hash, b.algorithm, b.size_bytes, b.mime_type, b.storage_path,
	COALESCE(a.title, ''), a.type
FROM artifact a
JOIN blob b ON b.hash = a.content_hash
WHERE a.id = ? AND a.deleted_at IS NULL
`, artifactID)

	var info BlobInfo
	err := row.Scan(&info.Hash, &info.Algorithm, &info.SizeBytes, &info.MIMEType, &info.StoragePath, &info.Title, &info.Type)
	if err == sql.ErrNoRows {
		return BlobInfo{}, false, nil
	}
	if err != nil {
		return BlobInfo{}, false, fmt.Errorf("get blob for artifact: %w", err)
	}
	return info, true, nil
}

func GetDetail(ctx context.Context, db *sql.DB, id string) (ArtifactDetail, bool, error) {
	row := db.QueryRowContext(ctx, `
SELECT a.id, a.type, COALESCE(a.title, ''), a.source, a.source_id, a.owner,
	a.captured_at, a.event_at, a.created_at, a.metadata_json,
	b.hash, b.algorithm, b.size_bytes, b.mime_type, b.storage_path,
	e.artifact_id IS NOT NULL AS has_text,
	e.extractor, e.extractor_version,
	COALESCE(length(e.text), 0) AS text_chars,
	e.text, e.created_at,
	d.artifact_id IS NOT NULL AS has_document,
	d.status, d.extractor, d.extractor_version, d.processing_time_ms,
	COALESCE(length(d.markdown), 0) AS markdown_chars,
	d.markdown, d.structured_json, d.metadata_json,
	d.warnings_json, d.errors_json, d.created_at,
	w.path, w.last_enqueued_job_id
FROM artifact a
JOIN blob b ON b.hash = a.content_hash
LEFT JOIN extracted_text e ON e.artifact_id = a.id
LEFT JOIN extracted_document d ON d.artifact_id = a.id
LEFT JOIN watch_file_state w ON w.source_id = a.source_id
WHERE a.id = ? AND a.deleted_at IS NULL
`, id)

	var d ArtifactDetail
	err := row.Scan(
		&d.ID, &d.Type, &d.Title, &d.Source, &d.SourceID, &d.Owner,
		&d.CapturedAt, &d.EventAt, &d.CreatedAt, &d.MetadataJSON,
		&d.BlobHash, &d.BlobAlgorithm, &d.BlobSizeBytes, &d.BlobMIMEType, &d.BlobStoragePath,
		&d.HasText, &d.TextExtractor, &d.TextExtractorVer, &d.TextChars, &d.Text, &d.TextCreatedAt,
		&d.HasDocument, &d.DocStatus, &d.DocExtractor, &d.DocExtractorVer, &d.DocProcessingTimeMS,
		&d.MarkdownChars, &d.Markdown, &d.StructuredJSON, &d.DocMetadataJSON,
		&d.WarningsJSON, &d.ErrorsJSON, &d.DocCreatedAt,
		&d.WatchPath, &d.LastEnqueuedJobID,
	)
	if err == sql.ErrNoRows {
		return ArtifactDetail{}, false, nil
	}
	if err != nil {
		return ArtifactDetail{}, false, fmt.Errorf("get artifact: %w", err)
	}

	chunks, err := getChunks(ctx, db, id)
	if err != nil {
		return ArtifactDetail{}, false, err
	}
	d.Chunks = chunks
	return d, true, nil
}

func getChunks(ctx context.Context, db *sql.DB, artifactID string) ([]ChunkSummary, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, ordinal, title, heading_path, page_start, page_end,
	char_start, char_end, COALESCE(length(text), 0), text, metadata_json
FROM artifact_chunk
WHERE artifact_id = ?
ORDER BY ordinal
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query chunks: %w", err)
	}
	defer rows.Close()

	var out []ChunkSummary
	for rows.Next() {
		var c ChunkSummary
		if err := rows.Scan(
			&c.ID, &c.Ordinal, &c.Title, &c.HeadingPath, &c.PageStart, &c.PageEnd,
			&c.CharStart, &c.CharEnd, &c.TextChars, &c.Text, &c.MetadataJSON,
		); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chunks: %w", err)
	}
	return out, nil
}

type ListOptions struct {
	Type  string
	Since string
	Limit int
}

func List(ctx context.Context, db *sql.DB, opts ListOptions) ([]Artifact, error) {
	limit := opts.Limit
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	args := []any{}
	where := []string{"a.deleted_at IS NULL"}
	if opts.Type != "" && opts.Type != "all" {
		where = append(where, "a.type = ?")
		args = append(args, opts.Type)
	}
	if opts.Since != "" {
		where = append(where, "(a.event_at >= ? OR a.created_at >= ?)")
		args = append(args, opts.Since, opts.Since)
	}
	args = append(args, limit)

	q := `
SELECT a.id, a.type, COALESCE(a.title, ''), a.event_at, a.created_at,
	e.artifact_id IS NOT NULL AS has_text,
	substr(e.text, 1, 180) AS text_preview,
	COALESCE(cc.chunk_count, 0) AS chunk_count
FROM artifact a
LEFT JOIN extracted_text e ON e.artifact_id = a.id
LEFT JOIN (
	SELECT artifact_id, COUNT(*) AS chunk_count
	FROM artifact_chunk
	GROUP BY artifact_id
) cc ON cc.artifact_id = a.id
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY a.created_at DESC
LIMIT ?`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query artifacts: %w", err)
	}
	defer rows.Close()

	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.Type, &a.Title, &a.EventAt, &a.CreatedAt, &a.HasText, &a.TextPreview, &a.ChunkCount); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts: %w", err)
	}
	return out, nil
}

func Recent(ctx context.Context, db *sql.DB, limit int) ([]Artifact, error) {
	if limit < 1 {
		limit = 10
	}
	rows, err := db.QueryContext(ctx, `
SELECT a.id, a.type, COALESCE(a.title, ''), a.event_at, a.created_at,
	e.artifact_id IS NOT NULL AS has_text,
	substr(e.text, 1, 180) AS text_preview,
	COALESCE(cc.chunk_count, 0) AS chunk_count
FROM artifact a
LEFT JOIN extracted_text e ON e.artifact_id = a.id
LEFT JOIN (
	SELECT artifact_id, COUNT(*) AS chunk_count
	FROM artifact_chunk
	GROUP BY artifact_id
) cc ON cc.artifact_id = a.id
WHERE a.deleted_at IS NULL
ORDER BY a.created_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent artifacts: %w", err)
	}
	defer rows.Close()

	var out []Artifact
	for rows.Next() {
		var artifact Artifact
		if err := rows.Scan(
			&artifact.ID,
			&artifact.Type,
			&artifact.Title,
			&artifact.EventAt,
			&artifact.CreatedAt,
			&artifact.HasText,
			&artifact.TextPreview,
			&artifact.ChunkCount,
		); err != nil {
			return nil, fmt.Errorf("scan recent artifact: %w", err)
		}
		out = append(out, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent artifacts: %w", err)
	}
	return out, nil
}

func Search(ctx context.Context, db *sql.DB, query string, limit int) ([]SearchResult, error) {
	if limit < 1 {
		limit = 10
	}
	rows, err := db.QueryContext(ctx, `
SELECT a.id, a.type, COALESCE(a.title, ''), a.event_at,
	snippet(artifact_fts, 1, '', '', ' ... ', 16)
FROM artifact_fts
JOIN search_document sd ON sd.rowid = artifact_fts.rowid
JOIN artifact a ON a.id = sd.artifact_id
WHERE artifact_fts MATCH ?
	AND a.deleted_at IS NULL
ORDER BY bm25(artifact_fts)
LIMIT ?
`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query artifact search: %w", err)
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		var result SearchResult
		if err := rows.Scan(
			&result.ID,
			&result.Type,
			&result.Title,
			&result.EventAt,
			&result.Snippet,
		); err != nil {
			return nil, fmt.Errorf("scan artifact search result: %w", err)
		}
		out = append(out, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact search results: %w", err)
	}
	return out, nil
}
