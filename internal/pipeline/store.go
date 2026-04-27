package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type artifactSnapshot struct {
	ID          string
	Type        string
	Title       string
	EventAt     string
	CreatedAt   string
	ContentHash string
	Text        string
}

func MarkStageRunning(ctx context.Context, db *sql.DB, artifactID, stage, hash string, now time.Time) error {
	_, err := db.ExecContext(ctx, `
INSERT INTO pipeline_stage (
	artifact_id, stage, status, input_hash, attempts, created_at, updated_at
) VALUES (?, ?, 'running', NULLIF(?, ''), 1, ?, ?)
ON CONFLICT(artifact_id, stage) DO UPDATE SET
	status = 'running',
	input_hash = excluded.input_hash,
	attempts = pipeline_stage.attempts + 1,
	last_error = NULL,
	updated_at = excluded.updated_at,
	finished_at = NULL
`,
		artifactID,
		stage,
		hash,
		formatTime(now),
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("mark pipeline stage running: %w", err)
	}
	return nil
}

func MarkStageSucceeded(ctx context.Context, db *sql.DB, artifactID, stage, hash string, now time.Time) error {
	_, err := db.ExecContext(ctx, `
INSERT INTO pipeline_stage (
	artifact_id, stage, status, input_hash, attempts, created_at, updated_at, finished_at
) VALUES (?, ?, 'succeeded', NULLIF(?, ''), 1, ?, ?, ?)
ON CONFLICT(artifact_id, stage) DO UPDATE SET
	status = 'succeeded',
	input_hash = excluded.input_hash,
	last_error = NULL,
	updated_at = excluded.updated_at,
	finished_at = excluded.finished_at
`,
		artifactID,
		stage,
		hash,
		formatTime(now),
		formatTime(now),
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("mark pipeline stage succeeded: %w", err)
	}
	return nil
}

func MarkStageFailed(ctx context.Context, db *sql.DB, artifactID, stage, hash string, stageErr error, now time.Time) error {
	message := ""
	if stageErr != nil {
		message = stageErr.Error()
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO pipeline_stage (
	artifact_id, stage, status, input_hash, attempts, last_error, created_at, updated_at, finished_at
) VALUES (?, ?, 'failed', NULLIF(?, ''), 1, NULLIF(?, ''), ?, ?, ?)
ON CONFLICT(artifact_id, stage) DO UPDATE SET
	status = 'failed',
	input_hash = excluded.input_hash,
	attempts = pipeline_stage.attempts + 1,
	last_error = excluded.last_error,
	updated_at = excluded.updated_at,
	finished_at = excluded.finished_at
`,
		artifactID,
		stage,
		hash,
		message,
		formatTime(now),
		formatTime(now),
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("mark pipeline stage failed: %w", err)
	}
	return nil
}

func LoadArtifactSnapshot(ctx context.Context, db *sql.DB, artifactID string) (artifactSnapshot, bool, error) {
	row := db.QueryRowContext(ctx, `
SELECT a.id, a.type, COALESCE(a.title, ''), COALESCE(a.event_at, ''),
	a.created_at, a.content_hash, COALESCE(e.text, '')
FROM artifact a
LEFT JOIN extracted_text e ON e.artifact_id = a.id
WHERE a.id = ?
	AND a.deleted_at IS NULL
`, artifactID)
	var snapshot artifactSnapshot
	if err := row.Scan(
		&snapshot.ID,
		&snapshot.Type,
		&snapshot.Title,
		&snapshot.EventAt,
		&snapshot.CreatedAt,
		&snapshot.ContentHash,
		&snapshot.Text,
	); err != nil {
		if err == sql.ErrNoRows {
			return artifactSnapshot{}, false, nil
		}
		return artifactSnapshot{}, false, fmt.Errorf("load artifact snapshot: %w", err)
	}
	return snapshot, true, nil
}

func UpsertEvidenceForArtifact(ctx context.Context, db *sql.DB, artifactID string, now time.Time) ([]Evidence, error) {
	rows, err := db.QueryContext(ctx, `
SELECT c.id, c.text, c.char_start, c.char_end,
	COALESCE(c.page_start, 0), COALESCE(c.page_end, 0),
	COALESCE(d.extractor, e.extractor, '')
FROM artifact_chunk c
LEFT JOIN extracted_document d ON d.artifact_id = c.artifact_id
LEFT JOIN extracted_text e ON e.artifact_id = c.artifact_id
WHERE c.artifact_id = ?
ORDER BY c.ordinal
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query artifact chunks for evidence: %w", err)
	}
	defer rows.Close()

	var evidence []Evidence
	for rows.Next() {
		var item Evidence
		item.ArtifactID = artifactID
		item.Kind = "chunk"
		if err := rows.Scan(
			&item.ChunkID,
			&item.Quote,
			&item.CharStart,
			&item.CharEnd,
			&item.PageStart,
			&item.PageEnd,
			&item.Extractor,
		); err != nil {
			return nil, fmt.Errorf("scan evidence chunk: %w", err)
		}
		item.Quote = truncateRunes(item.Quote, maxEvidenceQuote)
		item.ID = hashID("ev", artifactID, item.ChunkID, item.Quote)
		item.CreatedAt = formatTime(now)
		evidence = append(evidence, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence chunks: %w", err)
	}

	if len(evidence) == 0 {
		fallback, ok, err := fallbackTextEvidence(ctx, db, artifactID, now)
		if err != nil {
			return nil, err
		}
		if ok {
			evidence = append(evidence, fallback)
		}
	}

	for _, item := range evidence {
		provenanceJSON, err := evidenceProvenance(item)
		if err != nil {
			return nil, err
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO evidence (
	id, artifact_id, chunk_id, kind, quote, char_start, char_end,
	page_start, page_end, extractor, provenance_json, created_at
) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, ''), ?, ?)
ON CONFLICT(id) DO UPDATE SET
	quote = excluded.quote,
	char_start = excluded.char_start,
	char_end = excluded.char_end,
	page_start = excluded.page_start,
	page_end = excluded.page_end,
	extractor = excluded.extractor,
	provenance_json = excluded.provenance_json
`,
			item.ID,
			item.ArtifactID,
			item.ChunkID,
			item.Kind,
			item.Quote,
			item.CharStart,
			item.CharEnd,
			item.PageStart,
			item.PageEnd,
			item.Extractor,
			provenanceJSON,
			item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("upsert evidence: %w", err)
		}
	}

	return ListEvidence(ctx, db, artifactID)
}

func ListEvidence(ctx context.Context, db *sql.DB, artifactID string) ([]Evidence, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, artifact_id, COALESCE(chunk_id, ''), kind, quote,
	char_start, char_end, COALESCE(page_start, 0), COALESCE(page_end, 0),
	COALESCE(extractor, ''), provenance_json, created_at
FROM evidence
WHERE artifact_id = ?
ORDER BY char_start, id
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query evidence: %w", err)
	}
	defer rows.Close()

	var out []Evidence
	for rows.Next() {
		var item Evidence
		if err := rows.Scan(
			&item.ID,
			&item.ArtifactID,
			&item.ChunkID,
			&item.Kind,
			&item.Quote,
			&item.CharStart,
			&item.CharEnd,
			&item.PageStart,
			&item.PageEnd,
			&item.Extractor,
			&item.ProvenanceJSON,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan evidence: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence: %w", err)
	}
	return out, nil
}

func fallbackTextEvidence(ctx context.Context, db *sql.DB, artifactID string, now time.Time) (Evidence, bool, error) {
	row := db.QueryRowContext(ctx, `
SELECT e.text, e.extractor
FROM extracted_text e
WHERE e.artifact_id = ?
`, artifactID)
	var text string
	var extractor string
	if err := row.Scan(&text, &extractor); err != nil {
		if err == sql.ErrNoRows {
			return Evidence{}, false, nil
		}
		return Evidence{}, false, fmt.Errorf("query fallback evidence: %w", err)
	}
	text = truncateRunes(text, maxEvidenceQuote)
	if text == "" {
		return Evidence{}, false, nil
	}
	return Evidence{
		ID:         hashID("ev", artifactID, "extracted_text", text),
		ArtifactID: artifactID,
		Kind:       "extracted_text",
		Quote:      text,
		CharStart:  0,
		CharEnd:    len([]rune(text)),
		Extractor:  extractor,
		CreatedAt:  formatTime(now),
	}, true, nil
}

func evidenceProvenance(item Evidence) (string, error) {
	source := item.Kind
	if item.ChunkID != "" {
		source = "artifact_chunk"
	}
	data, err := json.Marshal(map[string]any{
		"source":    source,
		"chunk_id":  item.ChunkID,
		"extractor": item.Extractor,
	})
	if err != nil {
		return "", fmt.Errorf("encode evidence provenance: %w", err)
	}
	return string(data), nil
}

func ReplaceFacts(ctx context.Context, db *sql.DB, artifactID string, facts []Fact, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace facts: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM extracted_fact WHERE artifact_id = ?`, artifactID); err != nil {
		return fmt.Errorf("delete old facts: %w", err)
	}
	for _, fact := range facts {
		if fact.ID == "" {
			fact.ID = hashID("fact", fact.ArtifactID, fact.Type, fact.TextValue, fact.EvidenceID, fact.SourceType)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO extracted_fact (
	id, artifact_id, fact_type, value_json, text_value, evidence_id, quote,
	confidence, source_type, model_name, prompt_version, input_hash, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)
`,
			fact.ID,
			fact.ArtifactID,
			fact.Type,
			fact.ValueJSON,
			fact.TextValue,
			fact.EvidenceID,
			fact.Quote,
			fact.Confidence,
			fact.SourceType,
			fact.ModelName,
			fact.PromptVersion,
			fact.InputHash,
			formatTime(now),
			formatTime(now),
		); err != nil {
			return fmt.Errorf("insert fact: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace facts: %w", err)
	}
	return nil
}
