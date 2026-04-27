package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusDead      = "dead"
	StatusCancelled = "cancelled"
)

type Store struct {
	DB *sql.DB
}

type Job struct {
	ID          string
	Kind        string
	Status      string
	Priority    int
	PayloadJSON string
	ResultJSON  sql.NullString
	Attempts    int
	MaxAttempts int
	RunAfter    string
	LockedBy    sql.NullString
	LockedAt    sql.NullString
	LastError   sql.NullString
	CreatedAt   string
	UpdatedAt   string
	FinishedAt  sql.NullString
	DedupeKey   sql.NullString
}

type EnqueueParams struct {
	Kind        string
	Priority    int
	PayloadJSON string
	MaxAttempts int
	RunAfter    time.Time
	DedupeKey   string
}

type EnqueueResult struct {
	Job     Job
	Created bool
}

type permanentError struct {
	err error
}

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

func (e permanentError) Error() string {
	return e.err.Error()
}

func (e permanentError) Unwrap() error {
	return e.err
}

func IsPermanent(err error) bool {
	var target permanentError
	return errors.As(err, &target)
}

func (s Store) Enqueue(ctx context.Context, params EnqueueParams) (EnqueueResult, error) {
	if params.Kind == "" {
		return EnqueueResult{}, errors.New("job kind is required")
	}
	if params.PayloadJSON == "" {
		return EnqueueResult{}, errors.New("job payload_json is required")
	}
	if params.MaxAttempts < 1 {
		return EnqueueResult{}, errors.New("job max_attempts must be at least 1")
	}
	if params.RunAfter.IsZero() {
		params.RunAfter = time.Now().UTC()
	}

	if params.DedupeKey != "" {
		job, ok, err := s.findByDedupeKey(ctx, params.Kind, params.DedupeKey)
		if err != nil {
			return EnqueueResult{}, err
		}
		if ok {
			return EnqueueResult{Job: job, Created: false}, nil
		}
	}

	now := formatTime(time.Now())
	id, err := newID("job")
	if err != nil {
		return EnqueueResult{}, err
	}

	_, err = s.DB.ExecContext(ctx, `
INSERT INTO ingest_job (
	id, kind, status, priority, payload_json, attempts, max_attempts,
	run_after, created_at, updated_at, dedupe_key
) VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, NULLIF(?, ''))
`,
		id,
		params.Kind,
		StatusQueued,
		params.Priority,
		params.PayloadJSON,
		params.MaxAttempts,
		formatTime(params.RunAfter),
		now,
		now,
		params.DedupeKey,
	)
	if err != nil {
		if params.DedupeKey != "" && strings.Contains(strings.ToLower(err.Error()), "unique") {
			job, ok, findErr := s.findByDedupeKey(ctx, params.Kind, params.DedupeKey)
			if findErr != nil {
				return EnqueueResult{}, findErr
			}
			if ok {
				return EnqueueResult{Job: job, Created: false}, nil
			}
		}
		return EnqueueResult{}, fmt.Errorf("insert ingest_job: %w", err)
	}

	job, ok, err := s.Get(ctx, id)
	if err != nil {
		return EnqueueResult{}, err
	}
	if !ok {
		return EnqueueResult{}, fmt.Errorf("inserted job %s disappeared", id)
	}
	return EnqueueResult{Job: job, Created: true}, nil
}

func (s Store) Claim(ctx context.Context, workerID, kind string, now time.Time) (Job, bool, error) {
	if workerID == "" {
		return Job{}, false, errors.New("worker id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, fmt.Errorf("begin claim: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
UPDATE ingest_job
SET status = ?,
	attempts = attempts + 1,
	locked_by = ?,
	locked_at = ?,
	updated_at = ?
WHERE id = (
	SELECT id
	FROM ingest_job
	WHERE status IN (?, ?)
		AND run_after <= ?
		AND (? = '' OR kind = ?)
	ORDER BY priority DESC, run_after ASC, created_at ASC
	LIMIT 1
)
RETURNING id, kind, status, priority, payload_json, result_json, attempts,
	max_attempts, run_after, locked_by, locked_at, last_error, created_at,
	updated_at, finished_at, dedupe_key
`,
		StatusRunning,
		workerID,
		formatTime(now),
		formatTime(now),
		StatusQueued,
		StatusFailed,
		formatTime(now),
		kind,
		kind,
	)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("claim job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Job{}, false, fmt.Errorf("commit claim: %w", err)
	}
	return job, true, nil
}

func (s Store) Succeed(ctx context.Context, job Job, resultJSON string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `
UPDATE ingest_job
SET status = ?,
	result_json = NULLIF(?, ''),
	locked_by = NULL,
	locked_at = NULL,
	last_error = NULL,
	updated_at = ?,
	finished_at = ?
WHERE id = ?
`,
		StatusSucceeded,
		resultJSON,
		formatTime(now),
		formatTime(now),
		job.ID,
	)
	if err != nil {
		return fmt.Errorf("mark job succeeded: %w", err)
	}
	return nil
}

func (s Store) Fail(ctx context.Context, job Job, err error, now time.Time) error {
	if err == nil {
		return errors.New("job failure error is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	status := StatusFailed
	finishedAt := sql.NullString{}
	runAfter := now.Add(backoff(job.Attempts))
	if job.Attempts >= job.MaxAttempts || IsPermanent(err) {
		status = StatusDead
		finishedAt = sql.NullString{String: formatTime(now), Valid: true}
		runAfter = now
	}

	_, execErr := s.DB.ExecContext(ctx, `
UPDATE ingest_job
SET status = ?,
	run_after = ?,
	locked_by = NULL,
	locked_at = NULL,
	last_error = ?,
	updated_at = ?,
	finished_at = ?
WHERE id = ?
`,
		status,
		formatTime(runAfter),
		err.Error(),
		formatTime(now),
		nullableString(finishedAt),
		job.ID,
	)
	if execErr != nil {
		return fmt.Errorf("mark job failed: %w", execErr)
	}
	return nil
}

func (s Store) Get(ctx context.Context, id string) (Job, bool, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, kind, status, priority, payload_json, result_json, attempts,
	max_attempts, run_after, locked_by, locked_at, last_error, created_at,
	updated_at, finished_at, dedupe_key
FROM ingest_job
WHERE id = ?
`, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("get job: %w", err)
	}
	return job, true, nil
}

func (s Store) Recent(ctx context.Context, limit int) ([]Job, error) {
	if limit < 1 {
		limit = 10
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, kind, status, priority, payload_json, result_json, attempts,
	max_attempts, run_after, locked_by, locked_at, last_error, created_at,
	updated_at, finished_at, dedupe_key
FROM ingest_job
ORDER BY updated_at DESC, created_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent jobs: %w", err)
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent jobs: %w", err)
	}
	return out, nil
}

func (s Store) CountsByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT status, COUNT(*)
FROM ingest_job
GROUP BY status
`)
	if err != nil {
		return nil, fmt.Errorf("query job counts: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{
		StatusQueued:    0,
		StatusRunning:   0,
		StatusSucceeded: 0,
		StatusFailed:    0,
		StatusDead:      0,
		StatusCancelled: 0,
	}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan job count: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job counts: %w", err)
	}
	return counts, nil
}

func (s Store) findByDedupeKey(ctx context.Context, kind, dedupeKey string) (Job, bool, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, kind, status, priority, payload_json, result_json, attempts,
	max_attempts, run_after, locked_by, locked_at, last_error, created_at,
	updated_at, finished_at, dedupe_key
FROM ingest_job
WHERE kind = ?
	AND dedupe_key = ?
	AND status <> ?
ORDER BY created_at DESC
LIMIT 1
`, kind, dedupeKey, StatusCancelled)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("find job by dedupe key: %w", err)
	}
	return job, true, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var job Job
	err := row.Scan(
		&job.ID,
		&job.Kind,
		&job.Status,
		&job.Priority,
		&job.PayloadJSON,
		&job.ResultJSON,
		&job.Attempts,
		&job.MaxAttempts,
		&job.RunAfter,
		&job.LockedBy,
		&job.LockedAt,
		&job.LastError,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.FinishedAt,
		&job.DedupeKey,
	)
	return job, err
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := 5 * time.Second
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= 5*time.Minute {
			return 5 * time.Minute
		}
	}
	return delay
}

func nullableString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func newID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}
