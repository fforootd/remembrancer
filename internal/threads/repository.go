package threads

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Thread struct {
	ID          string
	Kind        string
	Title       string
	Summary     string
	DateStart   string
	DateEnd     string
	Status      string
	MemberCount int
	UpdatedAt   string
	CreatedAt   string
}

type Member struct {
	ArtifactID string
	Title      string
	Type       string
	EventAt    string
	Score      float64
	Source     string
	AddedAt    string
}

type FactSummary struct {
	Type    string
	Value   string
	Count   int
	Sources int
}

type Repository struct {
	DB *sql.DB
}

func (r Repository) RecentThreads(ctx context.Context, limit int) ([]Thread, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.DB.QueryContext(ctx, `
SELECT t.id, t.kind, t.title, COALESCE(t.summary, ''),
	COALESCE(t.date_start, ''), COALESCE(t.date_end, ''),
	t.status, COUNT(m.artifact_id) AS members,
	t.updated_at, t.created_at
FROM thread t
LEFT JOIN thread_member m ON m.thread_id = t.id
WHERE t.status = 'active'
GROUP BY t.id
ORDER BY t.updated_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent threads: %w", err)
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(
			&t.ID, &t.Kind, &t.Title, &t.Summary,
			&t.DateStart, &t.DateEnd, &t.Status, &t.MemberCount,
			&t.UpdatedAt, &t.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan thread: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r Repository) GetThread(ctx context.Context, id string) (Thread, bool, error) {
	row := r.DB.QueryRowContext(ctx, `
SELECT t.id, t.kind, t.title, COALESCE(t.summary, ''),
	COALESCE(t.date_start, ''), COALESCE(t.date_end, ''),
	t.status, (SELECT COUNT(*) FROM thread_member WHERE thread_id = t.id),
	t.updated_at, t.created_at
FROM thread t
WHERE t.id = ?
`, id)
	var t Thread
	if err := row.Scan(
		&t.ID, &t.Kind, &t.Title, &t.Summary,
		&t.DateStart, &t.DateEnd, &t.Status, &t.MemberCount,
		&t.UpdatedAt, &t.CreatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return Thread{}, false, nil
		}
		return Thread{}, false, fmt.Errorf("get thread: %w", err)
	}
	return t, true, nil
}

func (r Repository) ListMembers(ctx context.Context, threadID string) ([]Member, error) {
	rows, err := r.DB.QueryContext(ctx, `
SELECT m.artifact_id, COALESCE(a.title, m.artifact_id), COALESCE(a.type, ''),
	COALESCE(a.event_at, ''), m.score, m.source, m.added_at
FROM thread_member m
LEFT JOIN artifact a ON a.id = m.artifact_id
WHERE m.thread_id = ?
	AND (a.deleted_at IS NULL OR a.id IS NULL)
ORDER BY COALESCE(a.event_at, m.added_at) ASC
`, threadID)
	if err != nil {
		return nil, fmt.Errorf("query thread members: %w", err)
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(
			&m.ArtifactID, &m.Title, &m.Type, &m.EventAt,
			&m.Score, &m.Source, &m.AddedAt,
		); err != nil {
			return nil, fmt.Errorf("scan thread member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r Repository) ThreadFacts(ctx context.Context, threadID string) ([]FactSummary, error) {
	rows, err := r.DB.QueryContext(ctx, `
SELECT f.fact_type, f.text_value,
	COUNT(*) AS occurrences,
	COUNT(DISTINCT f.artifact_id) AS sources
FROM thread_member m
JOIN extracted_fact f ON f.artifact_id = m.artifact_id
WHERE m.thread_id = ?
	AND f.fact_type IN ('vendor', 'organization', 'person', 'amount', 'due_date', 'appointment', 'policy_number', 'account_number', 'document_title', 'requested_action')
	AND f.text_value <> ''
GROUP BY f.fact_type, LOWER(f.text_value)
ORDER BY sources DESC, occurrences DESC
LIMIT 30
`, threadID)
	if err != nil {
		return nil, fmt.Errorf("query thread facts: %w", err)
	}
	defer rows.Close()
	var out []FactSummary
	for rows.Next() {
		var f FactSummary
		if err := rows.Scan(&f.Type, &f.Value, &f.Count, &f.Sources); err != nil {
			return nil, fmt.Errorf("scan thread fact: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r Repository) ThreadsForArtifact(ctx context.Context, artifactID string) ([]Thread, error) {
	rows, err := r.DB.QueryContext(ctx, `
SELECT t.id, t.kind, t.title, COALESCE(t.summary, ''),
	COALESCE(t.date_start, ''), COALESCE(t.date_end, ''),
	t.status,
	(SELECT COUNT(*) FROM thread_member WHERE thread_id = t.id) AS members,
	t.updated_at, t.created_at
FROM thread t
JOIN thread_member m ON m.thread_id = t.id
WHERE m.artifact_id = ?
	AND t.status <> 'dismissed'
ORDER BY t.updated_at DESC
`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("query threads for artifact: %w", err)
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(
			&t.ID, &t.Kind, &t.Title, &t.Summary,
			&t.DateStart, &t.DateEnd, &t.Status, &t.MemberCount,
			&t.UpdatedAt, &t.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan artifact thread: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r Repository) SetStatus(ctx context.Context, id, status string, now time.Time) error {
	switch status {
	case "active", "archived", "dismissed":
	default:
		return fmt.Errorf("invalid thread status %q", status)
	}
	_, err := r.DB.ExecContext(ctx, `
UPDATE thread SET status = ?, updated_at = ? WHERE id = ?
`, status, now.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("update thread status: %w", err)
	}
	return nil
}

func (r Repository) ActiveThreadsTouchedIn(ctx context.Context, since, until time.Time, limit int) ([]Thread, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.DB.QueryContext(ctx, `
SELECT t.id, t.kind, t.title, COALESCE(t.summary, ''),
	COALESCE(t.date_start, ''), COALESCE(t.date_end, ''),
	t.status,
	(SELECT COUNT(*) FROM thread_member WHERE thread_id = t.id) AS members,
	t.updated_at, t.created_at
FROM thread t
WHERE t.status = 'active'
	AND t.updated_at >= ?
	AND t.updated_at <= ?
ORDER BY t.updated_at DESC
LIMIT ?
`, since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, fmt.Errorf("query active threads in range: %w", err)
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(
			&t.ID, &t.Kind, &t.Title, &t.Summary,
			&t.DateStart, &t.DateEnd, &t.Status, &t.MemberCount,
			&t.UpdatedAt, &t.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan range thread: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type KindGroup struct {
	Kind    string
	Threads []Thread
}

func GroupByKind(threads []Thread) []KindGroup {
	order := []string{"visit", "contract", "vendor_account", "vehicle", "school_year", "travel", "project", "other"}
	groups := map[string][]Thread{}
	for _, t := range threads {
		key := strings.TrimSpace(t.Kind)
		if key == "" {
			key = "other"
		}
		groups[key] = append(groups[key], t)
	}
	var out []KindGroup
	for _, k := range order {
		if len(groups[k]) == 0 {
			continue
		}
		out = append(out, KindGroup{Kind: k, Threads: groups[k]})
		delete(groups, k)
	}
	for k, v := range groups {
		if len(v) > 0 {
			out = append(out, KindGroup{Kind: k, Threads: v})
		}
	}
	return out
}
