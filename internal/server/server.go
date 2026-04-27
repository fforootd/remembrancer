package server

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	"zora/internal/artifacts"
	"zora/internal/config"
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

	lastScan, scanErr, hasLastScan := ingest.ScanResult{}, error(nil), false
	if s.ingest != nil {
		lastScan, scanErr, hasLastScan = s.ingest.LastScan()
	}
	ingestEnabled := s.cfg.Ingest.Enabled && s.ingest != nil

	data := indexData{
		AppName:         "Zora",
		UserName:        s.cfg.User.DisplayName,
		RuntimePath:     s.cfg.Paths.Runtime,
		ArchivePath:     s.cfg.Paths.Archive,
		InboxPath:       s.cfg.Paths.Inbox,
		SQLitePath:      s.cfg.SQLite.Path,
		LLMEnabled:      s.cfg.LLM.Enabled,
		LLMBaseURL:      s.cfg.LLM.BaseURL,
		LLMModel:        s.cfg.LLM.Model,
		IngestEnabled:   ingestEnabled,
		IngestWorkers:   s.cfg.Ingest.Workers,
		ScanInterval:    s.cfg.Ingest.ScanInterval.String(),
		SettleDuration:  s.cfg.Ingest.SettleDuration.String(),
		ExtractTimeout:  s.cfg.Ingest.ExtractTimeout.String(),
		JobCounts:       jobCounts,
		RecentJobs:      recentJobs,
		RecentArtifacts: recentArtifacts,
		WatchStats:      watchStats,
		LastScan:        lastScan,
		LastScanError:   errorString(scanErr),
		HasLastScan:     hasLastScan,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		s.logger.Error("render index", "error", err)
	}
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
	AppName         string
	UserName        string
	RuntimePath     string
	ArchivePath     string
	InboxPath       string
	SQLitePath      string
	LLMEnabled      bool
	LLMBaseURL      string
	LLMModel        string
	IngestEnabled   bool
	IngestWorkers   int
	ScanInterval    string
	SettleDuration  string
	ExtractTimeout  string
	JobCounts       map[string]int
	RecentJobs      []jobs.Job
	RecentArtifacts []artifacts.Artifact
	WatchStats      watchStats
	LastScan        ingest.ScanResult
	LastScanError   string
	HasLastScan     bool
}

type watchStats struct {
	Seen      int
	Supported int
	Ignored   int
}
