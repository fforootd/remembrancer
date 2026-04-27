package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"zora/internal/blobs"
	"zora/internal/jobs"
)

type Scanner struct {
	DB             *sql.DB
	Jobs           jobs.Store
	Inbox          string
	SettleDuration time.Duration
	MaxAttempts    int
	Now            func() time.Time
}

type ScanResult struct {
	Seen      int
	Enqueued  int
	Existing  int
	Ignored   int
	Settling  int
	Errored   int
	ScannedAt time.Time
}

func (s Scanner) Scan(ctx context.Context) (ScanResult, error) {
	if s.DB == nil {
		return ScanResult{}, fmt.Errorf("scanner database is required")
	}
	if s.Jobs.DB == nil {
		s.Jobs.DB = s.DB
	}
	if s.Inbox == "" {
		return ScanResult{}, fmt.Errorf("scanner inbox is required")
	}
	if s.MaxAttempts < 1 {
		s.MaxAttempts = 3
	}

	now := s.now()
	result := ScanResult{ScannedAt: now}
	root, err := filepath.Abs(s.Inbox)
	if err != nil {
		return result, fmt.Errorf("resolve inbox path: %w", err)
	}

	var firstErr error
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			result.Errored++
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			result.Errored++
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		result.Seen++
		absPath, err := filepath.Abs(path)
		if err != nil {
			result.Errored++
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}

		if now.Sub(info.ModTime()) < s.SettleDuration {
			result.Settling++
			if err := s.upsertWatchState(ctx, watchState{
				Path:          absPath,
				SizeBytes:     info.Size(),
				MTime:         info.ModTime(),
				LastSeenAt:    now,
				IgnoredReason: "settling",
				UpdatedAt:     now,
			}); err != nil && firstErr == nil {
				firstErr = err
			}
			return nil
		}

		artifactType, mimeType, ok := DetectFile(absPath)
		if !ok {
			result.Ignored++
			if err := s.upsertWatchState(ctx, watchState{
				Path:          absPath,
				SizeBytes:     info.Size(),
				MTime:         info.ModTime(),
				LastSeenAt:    now,
				IgnoredReason: "unsupported extension",
				UpdatedAt:     now,
			}); err != nil && firstErr == nil {
				firstErr = err
			}
			return nil
		}

		contentHash, size, err := blobs.HashFile(absPath)
		if err != nil {
			result.Errored++
			if stateErr := s.upsertWatchState(ctx, watchState{
				Path:          absPath,
				SizeBytes:     info.Size(),
				MTime:         info.ModTime(),
				LastSeenAt:    now,
				IgnoredReason: "read error: " + err.Error(),
				UpdatedAt:     now,
			}); stateErr != nil && firstErr == nil {
				firstErr = stateErr
			}
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}

		sourceID := SourceID(absPath, contentHash)
		payload := FilePayload{
			Path:        absPath,
			ContentHash: contentHash,
			SourceID:    sourceID,
			SizeBytes:   size,
			MTime:       formatTime(info.ModTime()),
			Type:        artifactType,
			MIMEType:    mimeType,
			Title:       TitleFromPath(absPath),
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			result.Errored++
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}

		enqueued, err := s.Jobs.Enqueue(ctx, jobs.EnqueueParams{
			Kind:        JobKindIngestFile,
			PayloadJSON: string(payloadJSON),
			MaxAttempts: s.MaxAttempts,
			RunAfter:    now,
			DedupeKey:   sourceID,
		})
		if err != nil {
			result.Errored++
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if enqueued.Created {
			result.Enqueued++
		} else {
			result.Existing++
		}

		if err := s.upsertWatchState(ctx, watchState{
			Path:              absPath,
			SizeBytes:         size,
			MTime:             info.ModTime(),
			ContentHash:       contentHash,
			SourceID:          sourceID,
			LastSeenAt:        now,
			LastEnqueuedJobID: enqueued.Job.ID,
			UpdatedAt:         now,
		}); err != nil && firstErr == nil {
			firstErr = err
		}
		return nil
	})
	if walkErr != nil {
		return result, walkErr
	}
	if firstErr != nil {
		return result, firstErr
	}
	return result, nil
}

type watchState struct {
	Path              string
	SizeBytes         int64
	MTime             time.Time
	ContentHash       string
	SourceID          string
	LastSeenAt        time.Time
	LastEnqueuedJobID string
	IgnoredReason     string
	UpdatedAt         time.Time
}

func (s Scanner) upsertWatchState(ctx context.Context, state watchState) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO watch_file_state (
	path, size_bytes, mtime, content_hash, source_id, last_seen_at,
	last_enqueued_job_id, ignored_reason, updated_at
) VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), ?)
ON CONFLICT(path) DO UPDATE SET
	size_bytes = excluded.size_bytes,
	mtime = excluded.mtime,
	content_hash = excluded.content_hash,
	source_id = excluded.source_id,
	last_seen_at = excluded.last_seen_at,
	last_enqueued_job_id = excluded.last_enqueued_job_id,
	ignored_reason = excluded.ignored_reason,
	updated_at = excluded.updated_at
`,
		state.Path,
		state.SizeBytes,
		formatTime(state.MTime),
		state.ContentHash,
		state.SourceID,
		formatTime(state.LastSeenAt),
		state.LastEnqueuedJobID,
		state.IgnoredReason,
		formatTime(state.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert watch file state: %w", err)
	}
	return nil
}

func (s Scanner) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
