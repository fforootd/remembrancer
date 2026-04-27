package server

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"zora/internal/artifacts"
	"zora/internal/config"
	"zora/internal/extract"
	"zora/internal/ingest"
	"zora/internal/jobs"
)

//go:embed templates/*.html
var templateFS embed.FS

type Server struct {
	cfg       config.Config
	database  *sql.DB
	logger    *slog.Logger
	templates *template.Template
	mux       *http.ServeMux
	ingest    *ingest.Service
}

func New(cfg config.Config, database *sql.DB, logger *slog.Logger, ingestService *ingest.Service) (*Server, error) {
	templates, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		cfg:       cfg,
		database:  database,
		logger:    logger,
		templates: templates,
		mux:       http.NewServeMux(),
		ingest:    ingestService,
	}
	server.routes()
	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.HandleFunc("GET /", s.index)
	s.mux.HandleFunc("POST /ingest/scan", s.scanIngest)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.database.PingContext(r.Context()); err != nil {
		s.logger.Error("health check failed", "error", err)
		http.Error(w, "sqlite unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK\n"))
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	jobStore := jobs.Store{DB: s.database}
	jobCounts, err := jobStore.CountsByStatus(r.Context())
	if err != nil {
		s.logger.Error("read job counts", "error", err)
		http.Error(w, "read job counts", http.StatusInternalServerError)
		return
	}
	recentJobs, err := jobStore.Recent(r.Context(), 8)
	if err != nil {
		s.logger.Error("read recent jobs", "error", err)
		http.Error(w, "read recent jobs", http.StatusInternalServerError)
		return
	}
	recentArtifacts, err := artifacts.Recent(r.Context(), s.database, 8)
	if err != nil {
		s.logger.Error("read recent artifacts", "error", err)
		http.Error(w, "read recent artifacts", http.StatusInternalServerError)
		return
	}
	watchStats, err := s.watchStats(r.Context())
	if err != nil {
		s.logger.Error("read watch stats", "error", err)
		http.Error(w, "read watch stats", http.StatusInternalServerError)
		return
	}
	chunkStats, err := s.chunkStats(r.Context())
	if err != nil {
		s.logger.Error("read chunk stats", "error", err)
		http.Error(w, "read chunk stats", http.StatusInternalServerError)
		return
	}
	lastExtractError, err := s.lastExtractError(r.Context())
	if err != nil {
		s.logger.Error("read last extraction error", "error", err)
		http.Error(w, "read last extraction error", http.StatusInternalServerError)
		return
	}

	lastScan, scanErr, hasLastScan := ingest.ScanResult{}, error(nil), false
	if s.ingest != nil {
		lastScan, scanErr, hasLastScan = s.ingest.LastScan()
	}
	ingestEnabled := s.cfg.Ingest.Enabled && s.ingest != nil
	doclingHealth := extract.HealthStatus{Detail: "not configured"}
	if s.cfg.Extract.Provider == "docling" {
		doclingHealth = extract.CheckDoclingHealth(r.Context(), s.cfg.Extract.Docling.BaseURL, s.cfg.Extract.Docling.APIKey, 250*time.Millisecond)
	}

	data := indexData{
		AppName:          "Zora",
		UserName:         s.cfg.User.DisplayName,
		RuntimePath:      s.cfg.Paths.Runtime,
		ArchivePath:      s.cfg.Paths.Archive,
		InboxPath:        s.cfg.Paths.Inbox,
		SQLitePath:       s.cfg.SQLite.Path,
		LLMEnabled:       s.cfg.LLM.Enabled,
		LLMBaseURL:       s.cfg.LLM.BaseURL,
		LLMModel:         s.cfg.LLM.Model,
		IngestEnabled:    ingestEnabled,
		IngestWorkers:    s.cfg.Ingest.Workers,
		ScanInterval:     s.cfg.Ingest.ScanInterval.String(),
		SettleDuration:   s.cfg.Ingest.SettleDuration.String(),
		ExtractProvider:  s.cfg.Extract.Provider,
		ExtractTimeout:   s.cfg.Extract.Timeout.String(),
		DoclingBaseURL:   s.cfg.Extract.Docling.BaseURL,
		DoclingHealth:    doclingHealth,
		LastExtractError: lastExtractError,
		JobCounts:        jobCounts,
		RecentJobs:       recentJobs,
		RecentArtifacts:  recentArtifacts,
		WatchStats:       watchStats,
		ChunkStats:       chunkStats,
		LastScan:         lastScan,
		LastScanError:    errorString(scanErr),
		HasLastScan:      hasLastScan,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		s.logger.Error("render index", "error", err)
	}
}

func (s *Server) chunkStats(ctx context.Context) (chunkStats, error) {
	var stats chunkStats
	err := s.database.QueryRowContext(ctx, `
SELECT COUNT(*), COUNT(DISTINCT artifact_id)
FROM artifact_chunk
`).Scan(&stats.Chunks, &stats.Artifacts)
	if err != nil {
		return chunkStats{}, err
	}
	return stats, nil
}

func (s *Server) lastExtractError(ctx context.Context) (string, error) {
	var lastError sql.NullString
	err := s.database.QueryRowContext(ctx, `
SELECT last_error
FROM ingest_job
WHERE kind = ?
	AND last_error IS NOT NULL
	AND last_error <> ''
ORDER BY updated_at DESC, created_at DESC
LIMIT 1
`, ingest.JobKindIngestFile).Scan(&lastError)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !lastError.Valid {
		return "", nil
	}
	return lastError.String, nil
}

func (s *Server) scanIngest(w http.ResponseWriter, r *http.Request) {
	if s.ingest == nil || !s.cfg.Ingest.Enabled {
		http.Error(w, "ingest is disabled", http.StatusServiceUnavailable)
		return
	}
	if _, err := s.ingest.Scan(r.Context()); err != nil {
		s.logger.Error("manual ingest scan", "error", err)
		http.Error(w, fmt.Sprintf("scan failed: %v", err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) watchStats(ctx context.Context) (watchStats, error) {
	var stats watchStats
	err := s.database.QueryRowContext(ctx, `
SELECT
	COUNT(*),
	COALESCE(SUM(CASE WHEN ignored_reason IS NULL THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ignored_reason IS NOT NULL THEN 1 ELSE 0 END), 0)
FROM watch_file_state
`).Scan(&stats.Seen, &stats.Supported, &stats.Ignored)
	if err != nil {
		return watchStats{}, err
	}
	return stats, nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type indexData struct {
	AppName          string
	UserName         string
	RuntimePath      string
	ArchivePath      string
	InboxPath        string
	SQLitePath       string
	LLMEnabled       bool
	LLMBaseURL       string
	LLMModel         string
	IngestEnabled    bool
	IngestWorkers    int
	ScanInterval     string
	SettleDuration   string
	ExtractProvider  string
	ExtractTimeout   string
	DoclingBaseURL   string
	DoclingHealth    extract.HealthStatus
	LastExtractError string
	JobCounts        map[string]int
	RecentJobs       []jobs.Job
	RecentArtifacts  []artifacts.Artifact
	WatchStats       watchStats
	ChunkStats       chunkStats
	LastScan         ingest.ScanResult
	LastScanError    string
	HasLastScan      bool
}

type watchStats struct {
	Seen      int
	Supported int
	Ignored   int
}

type chunkStats struct {
	Chunks    int
	Artifacts int
}
