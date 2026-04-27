package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"zora/internal/db"
)

func TestConcurrentClaimsProcessEachJobOnce(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 25; i++ {
		if _, err := store.Enqueue(ctx, EnqueueParams{
			Kind:        "ingest.file",
			PayloadJSON: `{"n":1}`,
			MaxAttempts: 3,
			RunAfter:    now,
			DedupeKey:   "file-" + strconv.Itoa(i),
		}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	seen := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for {
				job, ok, err := store.Claim(ctx, "worker-"+strconv.Itoa(worker), "ingest.file", now)
				if err != nil {
					t.Errorf("Claim: %v", err)
					return
				}
				if !ok {
					return
				}
				mu.Lock()
				if seen[job.ID] {
					t.Errorf("job %s claimed twice", job.ID)
				}
				seen[job.ID] = true
				mu.Unlock()
				if err := store.Succeed(ctx, job, `{"ok":true}`, now); err != nil {
					t.Errorf("Succeed: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	if len(seen) != 25 {
		t.Fatalf("processed %d jobs, want 25", len(seen))
	}

	counts, err := store.CountsByStatus(ctx)
	if err != nil {
		t.Fatalf("CountsByStatus: %v", err)
	}
	if counts[StatusSucceeded] != 25 {
		t.Fatalf("succeeded count = %d", counts[StatusSucceeded])
	}
}

func TestFailRetriesThenDeadLetters(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	enqueued, err := store.Enqueue(ctx, EnqueueParams{
		Kind:        "ingest.file",
		PayloadJSON: `{}`,
		MaxAttempts: 2,
		RunAfter:    now,
		DedupeKey:   "retry-file",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	job, ok, err := store.Claim(ctx, "worker-1", "ingest.file", now)
	if err != nil || !ok {
		t.Fatalf("first Claim ok=%v err=%v", ok, err)
	}
	if err := store.Fail(ctx, job, errTestFailure, now); err != nil {
		t.Fatalf("first Fail: %v", err)
	}
	job, ok, err = store.Get(ctx, enqueued.Job.ID)
	if err != nil || !ok {
		t.Fatalf("Get after first fail ok=%v err=%v", ok, err)
	}
	if job.Status != StatusFailed {
		t.Fatalf("status after first fail = %q", job.Status)
	}

	job, ok, err = store.Claim(ctx, "worker-1", "ingest.file", now.Add(10*time.Second))
	if err != nil || !ok {
		t.Fatalf("second Claim ok=%v err=%v", ok, err)
	}
	if err := store.Fail(ctx, job, errTestFailure, now.Add(10*time.Second)); err != nil {
		t.Fatalf("second Fail: %v", err)
	}
	job, ok, err = store.Get(ctx, enqueued.Job.ID)
	if err != nil || !ok {
		t.Fatalf("Get after second fail ok=%v err=%v", ok, err)
	}
	if job.Status != StatusDead {
		t.Fatalf("status after second fail = %q", job.Status)
	}
	if !job.FinishedAt.Valid {
		t.Fatal("dead job should have finished_at")
	}
}

func TestPermanentFailureDeadLettersImmediately(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	if _, err := store.Enqueue(ctx, EnqueueParams{
		Kind:        "ingest.file",
		PayloadJSON: `{}`,
		MaxAttempts: 3,
		RunAfter:    now,
		DedupeKey:   "permanent-file",
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, ok, err := store.Claim(ctx, "worker-1", "ingest.file", now)
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if err := store.Fail(ctx, job, Permanent(errTestFailure), now); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	job, ok, err = store.Get(ctx, job.ID)
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v", ok, err)
	}
	if job.Status != StatusDead {
		t.Fatalf("status = %q", job.Status)
	}
}

var errTestFailure = errors.New("boom")

func newTestStore(t *testing.T) Store {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "main.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(context.Background(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return Store{DB: database}
}
