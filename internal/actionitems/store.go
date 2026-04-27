package actionitems

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type Repository struct {
	DB *sql.DB
}

type CreateRunParams struct {
	PeriodStart     time.Time
	PeriodEnd       time.Time
	Title           string
	SourceQueryJSON string
	ModelName       string
	PromptVersion   string
	Items           []ValidatedItem
	Now             func() time.Time
}

type RunSummary struct {
	ID            string
	Title         string
	PeriodStart   string
	PeriodEnd     string
	ModelName     string
	PromptVersion string
	CreatedAt     string
	ItemCount     int
}

type Run struct {
	ID              string
	PeriodStart     string
	PeriodEnd       string
	Title           string
	SourceQueryJSON string
	ModelName       string
	PromptVersion   string
	CreatedAt       string
	Items           []RunItem
}

type RunItem struct {
	ID           string
	Category     string
	Title        string
	Summary      string
	WhyItMatters string
	ActionText   string
	DueAt        string
	Confidence   sql.NullFloat64
	SourceStatus string
	SortOrder    int
	CreatedAt    string
	ThreadID     string
	ThreadTitle  string
	ThreadKind   string
	Artifacts    []RunArtifact
}

type RunArtifact struct {
	ID      string
	Title   string
	Type    string
	Snippet string
}

func (r Repository) CreateRun(ctx context.Context, params CreateRunParams) (Run, error) {
	if r.DB == nil {
		return Run{}, fmt.Errorf("action item repository database is required")
	}
	if params.PromptVersion == "" {
		params.PromptVersion = PromptVersion
	}
	if params.Title == "" {
		params.Title = fmt.Sprintf("Action items: %s to %s", dateOnly(params.PeriodStart), dateOnly(params.PeriodEnd))
	}
	if params.SourceQueryJSON == "" {
		params.SourceQueryJSON = "{}"
	}
	now := time.Now().UTC()
	if params.Now != nil {
		now = params.Now().UTC()
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, fmt.Errorf("begin action item run transaction: %w", err)
	}
	defer tx.Rollback()

	runID, err := newID("brf")
	if err != nil {
		return Run{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO briefing (
	id, period_start, period_end, title, source_query_json,
	model_name, prompt_version, created_at
) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?)
`,
		runID,
		formatTime(params.PeriodStart),
		formatTime(params.PeriodEnd),
		params.Title,
		params.SourceQueryJSON,
		params.ModelName,
		params.PromptVersion,
		formatTime(now),
	); err != nil {
		return Run{}, fmt.Errorf("insert briefing: %w", err)
	}

	for index, item := range params.Items {
		itemID, err := newID("bri")
		if err != nil {
			return Run{}, err
		}
		if item.SortOrder < 0 {
			item.SortOrder = index
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO briefing_item (
	id, briefing_id, category, title, summary, why_it_matters,
	action_text, due_at, confidence, source_status, sort_order, created_at
) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?)
`,
			itemID,
			runID,
			item.Category,
			item.Title,
			item.Summary,
			item.WhyItMatters,
			item.ActionText,
			item.DueAt,
			item.Confidence,
			item.SourceStatus,
			item.SortOrder,
			formatTime(now),
		); err != nil {
			return Run{}, fmt.Errorf("insert briefing item: %w", err)
		}

		snippetsByArtifact := snippetsByArtifact(item.EvidenceSnippets)
		for _, artifactID := range item.ArtifactIDs {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO briefing_item_artifact (briefing_item_id, artifact_id, snippet)
VALUES (?, ?, NULLIF(?, ''))
`, itemID, artifactID, snippetsByArtifact[artifactID]); err != nil {
				return Run{}, fmt.Errorf("insert briefing item artifact: %w", err)
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `
UPDATE briefing_item AS bi
SET thread_id = (
	SELECT tm.thread_id
	FROM briefing_item_artifact bia
	JOIN thread_member tm ON tm.artifact_id = bia.artifact_id
	JOIN thread t ON t.id = tm.thread_id
	WHERE bia.briefing_item_id = bi.id
		AND t.status = 'active'
	GROUP BY tm.thread_id
	ORDER BY COUNT(*) DESC, MAX(t.updated_at) DESC
	LIMIT 1
)
WHERE bi.briefing_id = ?
	AND bi.thread_id IS NULL
`, runID); err != nil {
		return Run{}, fmt.Errorf("resolve thread_id for briefing items: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit action item run transaction: %w", err)
	}
	run, ok, err := r.GetRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return Run{}, fmt.Errorf("created action item run %s disappeared", runID)
	}
	return run, nil
}

func (r Repository) GetRun(ctx context.Context, id string) (Run, bool, error) {
	if r.DB == nil {
		return Run{}, false, fmt.Errorf("action item repository database is required")
	}
	row := r.DB.QueryRowContext(ctx, `
SELECT id, period_start, period_end, title, source_query_json,
	COALESCE(model_name, ''), COALESCE(prompt_version, ''), created_at
FROM briefing
WHERE id = ?
`, id)
	var run Run
	if err := row.Scan(
		&run.ID,
		&run.PeriodStart,
		&run.PeriodEnd,
		&run.Title,
		&run.SourceQueryJSON,
		&run.ModelName,
		&run.PromptVersion,
		&run.CreatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return Run{}, false, nil
		}
		return Run{}, false, fmt.Errorf("get action item run: %w", err)
	}

	items, err := r.runItems(ctx, id)
	if err != nil {
		return Run{}, false, err
	}
	run.Items = items
	return run, true, nil
}

func (r Repository) RecentRuns(ctx context.Context, limit int) ([]RunSummary, error) {
	if r.DB == nil {
		return nil, fmt.Errorf("action item repository database is required")
	}
	if limit < 1 {
		limit = 5
	}
	rows, err := r.DB.QueryContext(ctx, `
SELECT b.id, b.title, b.period_start, b.period_end,
	COALESCE(b.model_name, ''), COALESCE(b.prompt_version, ''), b.created_at,
	COUNT(i.id)
FROM briefing b
LEFT JOIN briefing_item i ON i.briefing_id = b.id
GROUP BY b.id
ORDER BY b.created_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent action item runs: %w", err)
	}
	defer rows.Close()

	var out []RunSummary
	for rows.Next() {
		var summary RunSummary
		if err := rows.Scan(
			&summary.ID,
			&summary.Title,
			&summary.PeriodStart,
			&summary.PeriodEnd,
			&summary.ModelName,
			&summary.PromptVersion,
			&summary.CreatedAt,
			&summary.ItemCount,
		); err != nil {
			return nil, fmt.Errorf("scan recent action item run: %w", err)
		}
		out = append(out, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent action item runs: %w", err)
	}
	return out, nil
}

func (r Repository) runItems(ctx context.Context, runID string) ([]RunItem, error) {
	rows, err := r.DB.QueryContext(ctx, `
SELECT i.id, i.category, i.title, i.summary, COALESCE(i.why_it_matters, ''),
	COALESCE(i.action_text, ''), COALESCE(i.due_at, ''), i.confidence,
	i.source_status, i.sort_order, i.created_at,
	COALESCE(i.thread_id, ''), COALESCE(t.title, ''), COALESCE(t.kind, '')
FROM briefing_item i
LEFT JOIN thread t ON t.id = i.thread_id
WHERE i.briefing_id = ?
ORDER BY i.sort_order, i.created_at
`, runID)
	if err != nil {
		return nil, fmt.Errorf("query action item run items: %w", err)
	}
	defer rows.Close()

	var out []RunItem
	for rows.Next() {
		var item RunItem
		if err := rows.Scan(
			&item.ID,
			&item.Category,
			&item.Title,
			&item.Summary,
			&item.WhyItMatters,
			&item.ActionText,
			&item.DueAt,
			&item.Confidence,
			&item.SourceStatus,
			&item.SortOrder,
			&item.CreatedAt,
			&item.ThreadID,
			&item.ThreadTitle,
			&item.ThreadKind,
		); err != nil {
			return nil, fmt.Errorf("scan action item run item: %w", err)
		}
		artifacts, err := r.itemArtifacts(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.Artifacts = artifacts
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate action item run items: %w", err)
	}
	return out, nil
}

func (r Repository) itemArtifacts(ctx context.Context, itemID string) ([]RunArtifact, error) {
	rows, err := r.DB.QueryContext(ctx, `
SELECT a.id, COALESCE(a.title, ''), a.type, COALESCE(bia.snippet, '')
FROM briefing_item_artifact bia
JOIN artifact a ON a.id = bia.artifact_id
WHERE bia.briefing_item_id = ?
ORDER BY a.event_at DESC, a.created_at DESC
`, itemID)
	if err != nil {
		return nil, fmt.Errorf("query action item artifacts: %w", err)
	}
	defer rows.Close()

	var out []RunArtifact
	for rows.Next() {
		var artifact RunArtifact
		if err := rows.Scan(&artifact.ID, &artifact.Title, &artifact.Type, &artifact.Snippet); err != nil {
			return nil, fmt.Errorf("scan action item artifact: %w", err)
		}
		out = append(out, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate action item artifacts: %w", err)
	}
	return out, nil
}

func SourceQueryJSON(start, end time.Time, candidates []Candidate) (string, error) {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ArtifactID)
	}
	data, err := json.Marshal(map[string]any{
		"kind":          "action_items",
		"period_start":  formatTime(start),
		"period_end":    formatTime(end),
		"candidate_ids": ids,
	})
	if err != nil {
		return "", fmt.Errorf("encode source query: %w", err)
	}
	return string(data), nil
}

func snippetsByArtifact(snippets []EvidenceSnippet) map[string]string {
	out := map[string]string{}
	for _, snippet := range snippets {
		if _, exists := out[snippet.ArtifactID]; exists {
			continue
		}
		out[snippet.ArtifactID] = snippet.Quote
	}
	return out
}

func dateOnly(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func newID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}
