package ingest

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"zora/internal/jobs"
)

type Service struct {
	Scanner      Scanner
	Jobs         jobs.Store
	Handler      FileHandler
	ScanInterval time.Duration
	Workers      int
	Logger       *slog.Logger

	mu             sync.Mutex
	lastScan       ScanResult
	lastScanErr    error
	lastScanExists bool
}

func (s *Service) Start(ctx context.Context) {
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	if s.Workers < 1 {
		s.Workers = 1
	}
	if s.ScanInterval <= 0 {
		s.ScanInterval = 30 * time.Second
	}

	go s.scanLoop(ctx)
	for i := 0; i < s.Workers; i++ {
		workerID := workerID(i)
		go s.workerLoop(ctx, workerID)
	}
}

func (s *Service) Scan(ctx context.Context) (ScanResult, error) {
	result, err := s.Scanner.Scan(ctx)
	s.mu.Lock()
	s.lastScan = result
	s.lastScanErr = err
	s.lastScanExists = true
	s.mu.Unlock()
	return result, err
}

func (s *Service) LastScan() (ScanResult, error, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastScan, s.lastScanErr, s.lastScanExists
}

func (s *Service) WorkOne(ctx context.Context, workerID string) (bool, error) {
	job, ok, err := s.Jobs.Claim(ctx, workerID, JobKindIngestFile, time.Now().UTC())
	if err != nil || !ok {
		return ok, err
	}

	result, handleErr := s.Handler.HandleJob(ctx, job)
	if handleErr != nil {
		if err := s.Jobs.Fail(ctx, job, handleErr, time.Now().UTC()); err != nil {
			return true, err
		}
		return true, handleErr
	}
	if err := s.Jobs.Succeed(ctx, job, result, time.Now().UTC()); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Service) scanLoop(ctx context.Context) {
	s.runScanAndLog(ctx)

	ticker := time.NewTicker(s.ScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runScanAndLog(ctx)
		}
	}
}

func (s *Service) runScanAndLog(ctx context.Context) {
	result, err := s.Scan(ctx)
	if err != nil {
		s.Logger.Warn("ingest scan failed", "error", err)
		return
	}
	s.Logger.Info("ingest scan complete", "seen", result.Seen, "enqueued", result.Enqueued, "existing", result.Existing, "ignored", result.Ignored, "settling", result.Settling)
}

func (s *Service) workerLoop(ctx context.Context, workerID string) {
	idle := 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		worked, err := s.WorkOne(ctx, workerID)
		if err != nil {
			s.Logger.Warn("ingest worker job failed", "worker", workerID, "error", err)
		}
		if worked {
			continue
		}

		timer := time.NewTimer(idle)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func workerID(index int) string {
	return "ingest-worker-" + strconv.Itoa(index+1)
}
