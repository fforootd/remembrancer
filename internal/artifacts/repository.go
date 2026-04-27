package artifacts

import (
	"context"
	"database/sql"
	"fmt"
)

type Artifact struct {
	ID          string
	Type        string
	Title       string
	EventAt     sql.NullString
	CreatedAt   string
	HasText     bool
	TextPreview sql.NullString
}

type SearchResult struct {
	ID      string
	Type    string
	Title   string
	EventAt sql.NullString
	Snippet string
}

func Recent(ctx context.Context, db *sql.DB, limit int) ([]Artifact, error) {
	if limit < 1 {
		limit = 10
	}
	rows, err := db.QueryContext(ctx, `
SELECT a.id, a.type, COALESCE(a.title, ''), a.event_at, a.created_at,
	e.artifact_id IS NOT NULL AS has_text,
	substr(e.text, 1, 180) AS text_preview
FROM artifact a
LEFT JOIN extracted_text e ON e.artifact_id = a.id
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
