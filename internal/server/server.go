package server

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"zora/internal/actionitems"
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
	reasoner  actionitems.Reasoner
}

type Option func(*Server)

func WithActionItemReasoner(reasoner actionitems.Reasoner) Option {
	return func(server *Server) {
		server.reasoner = reasoner
	}
}

func New(cfg config.Config, database *sql.DB, logger *slog.Logger, ingestService *ingest.Service, options ...Option) (*Server, error) {
	templates, err := template.New("zora").Funcs(template.FuncMap{
		"humanBytes": humanBytes,
		"prettyJSON": prettyJSON,
	}).ParseFS(templateFS, "templates/*.html")
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
	for _, option := range options {
		option(server)
	}
	if cfg.LLM.Enabled && server.reasoner == nil {
		server.reasoner = actionitems.OllamaClient{
			BaseURL:         cfg.LLM.BaseURL,
			Model:           cfg.LLM.Model,
			Timeout:         cfg.LLM.Timeout,
			ContextTokens:   cfg.LLM.ContextTokens,
			MaxOutputTokens: cfg.LLM.MaxOutputTokens,
			Temperature:     cfg.LLM.Temperature,
			HTTPClient:      &http.Client{Timeout: cfg.LLM.Timeout},
		}
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
	s.mux.HandleFunc("GET /action-items", s.actionItems)
	s.mux.HandleFunc("POST /action-items", s.generateActionItems)
	s.mux.HandleFunc("GET /briefings/{id}", s.briefing)
	s.mux.HandleFunc("GET /artifacts/{id}", s.artifact)
	s.mux.HandleFunc("GET /jobs/{id}", s.jobDetail)
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
	recentActionRuns, err := actionitems.Repository{DB: s.database}.RecentRuns(r.Context(), 5)
	if err != nil {
		s.logger.Error("read recent action item runs", "error", err)
		http.Error(w, "read recent action item runs", http.StatusInternalServerError)
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
		RecentActionRuns: recentActionRuns,
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

func (s *Server) actionItems(w http.ResponseWriter, r *http.Request) {
	start, end := defaultActionItemPeriod(time.Now().UTC())
	data := actionItemsData{
		AppName:         "Zora",
		UserName:        s.cfg.User.DisplayName,
		LLMEnabled:      s.cfg.LLM.Enabled,
		LLMModel:        s.cfg.LLM.Model,
		PeriodStartDate: start.Format("2006-01-02"),
		PeriodEndDate:   end.Format("2006-01-02"),
	}
	s.render(w, "action_items.html", data)
}

func (s *Server) generateActionItems(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	start, end, err := parseDateRange(r.FormValue("period_start"), r.FormValue("period_end"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	candidates, err := actionitems.Selector{DB: s.database}.Select(r.Context(), start, end)
	if err != nil {
		s.logger.Error("select action item candidates", "error", err)
		http.Error(w, "select action item candidates", http.StatusInternalServerError)
		return
	}

	if !s.cfg.LLM.Enabled {
		data := actionItemsData{
			AppName:         "Zora",
			UserName:        s.cfg.User.DisplayName,
			LLMEnabled:      false,
			LLMModel:        s.cfg.LLM.Model,
			PeriodStartDate: start.Format("2006-01-02"),
			PeriodEndDate:   end.Format("2006-01-02"),
			Candidates:      candidates,
			Previewed:       true,
		}
		s.render(w, "action_items.html", data)
		return
	}
	if s.reasoner == nil {
		http.Error(w, "LLM is enabled but no reasoner is configured", http.StatusServiceUnavailable)
		return
	}

	generated, err := s.reasoner.ExtractActionItems(r.Context(), actionitems.Request{
		PeriodStart:   start,
		PeriodEnd:     end,
		PromptVersion: actionitems.PromptVersion,
		Candidates:    candidates,
	})
	if err != nil {
		s.logger.Error("generate action items", "error", err)
		http.Error(w, "generate action items", http.StatusBadGateway)
		return
	}
	validated := actionitems.ValidateGenerated(generated, candidates)
	sourceQueryJSON, err := actionitems.SourceQueryJSON(start, end, candidates)
	if err != nil {
		s.logger.Error("encode action item source query", "error", err)
		http.Error(w, "encode action item source query", http.StatusInternalServerError)
		return
	}
	run, err := actionitems.Repository{DB: s.database}.CreateRun(r.Context(), actionitems.CreateRunParams{
		PeriodStart:     start,
		PeriodEnd:       end,
		SourceQueryJSON: sourceQueryJSON,
		ModelName:       s.cfg.LLM.Model,
		PromptVersion:   actionitems.PromptVersion,
		Items:           validated,
	})
	if err != nil {
		s.logger.Error("persist action items", "error", err)
		http.Error(w, "persist action items", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/briefings/"+run.ID, http.StatusSeeOther)
}

func (s *Server) briefing(w http.ResponseWriter, r *http.Request) {
	run, ok, err := actionitems.Repository{DB: s.database}.GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		s.logger.Error("read briefing", "error", err)
		http.Error(w, "read briefing", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, "briefing.html", briefingData{
		AppName: "Zora",
		Run:     run,
	})
}

func (s *Server) artifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	detail, ok, err := artifacts.GetDetail(r.Context(), s.database, id)
	if err != nil {
		s.logger.Error("read artifact", "error", err)
		http.Error(w, "read artifact", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	const previewMax = 4096
	mdPreview, mdTrunc := previewString(detail.Markdown, previewMax)
	txtPreview, txtTrunc := previewString(detail.Text, previewMax)

	s.render(w, "artifact.html", artifactData{
		AppName:           "Zora",
		Artifact:          detail,
		Warnings:          parseStringList(detail.WarningsJSON),
		Errors:            parseStringList(detail.ErrorsJSON),
		MarkdownPreview:   mdPreview,
		MarkdownTruncated: mdTrunc,
		TextPreview:       txtPreview,
		TextTruncated:     txtTrunc,
		DocStatusClass:    statusClass(detail.DocStatus.String),
	})
}

func (s *Server) jobDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	jobStore := jobs.Store{DB: s.database}
	job, ok, err := jobStore.Get(r.Context(), id)
	if err != nil {
		s.logger.Error("read job", "error", err)
		http.Error(w, "read job", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	data := jobData{
		AppName: "Zora",
		Job:     job,
	}

	if job.Kind == ingest.JobKindIngestFile && job.PayloadJSON != "" {
		var payload ingest.FilePayload
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err == nil {
			data.Payload = payload
			data.HasPayload = true
			if payload.SourceID != "" {
				data.ArtifactID = ingest.ArtifactID(payload.SourceID)
				data.ArtifactExists = artifactExists(r.Context(), s.database, data.ArtifactID)
			}
		}
	}

	if job.ResultJSON.Valid && job.ResultJSON.String != "" {
		var result ingest.JobResult
		if err := json.Unmarshal([]byte(job.ResultJSON.String), &result); err == nil {
			data.Result = result
			data.HasResult = true
			data.ResultDoclingClass = statusClass(result.DoclingStatus)
			if !data.ArtifactExists && result.ArtifactID != "" {
				data.ArtifactID = result.ArtifactID
				data.ArtifactExists = artifactExists(r.Context(), s.database, result.ArtifactID)
			}
		}
	}

	s.render(w, "job.html", data)
}

func artifactExists(ctx context.Context, db *sql.DB, id string) bool {
	if id == "" {
		return false
	}
	var one int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM artifact WHERE id = ? AND deleted_at IS NULL`, id).Scan(&one)
	return err == nil
}

func previewString(ns sql.NullString, max int) (string, bool) {
	if !ns.Valid {
		return "", false
	}
	if len(ns.String) <= max {
		return ns.String, false
	}
	return ns.String[:max], true
}

func parseStringList(ns sql.NullString) []string {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(ns.String), &out); err != nil {
		return nil
	}
	return out
}

func statusClass(status string) string {
	switch status {
	case "succeeded", "success", "ok":
		return "succeeded"
	case "failed", "dead", "error":
		return "failed"
	case "running", "pending":
		return "running"
	case "queued":
		return "queued"
	case "cancelled":
		return "cancelled"
	case "warn", "warning", "partial":
		return "warn"
	default:
		return ""
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func prettyJSON(s string) string {
	if s == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
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

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("render template", "template", name, "error", err)
	}
}

func parseDateRange(startValue, endValue string) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("2006-01-02", startValue, time.UTC)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("period_start must be YYYY-MM-DD")
	}
	end, err := time.ParseInLocation("2006-01-02", endValue, time.UTC)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("period_end must be YYYY-MM-DD")
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("period_end must be after period_start")
	}
	return start, end, nil
}

func defaultActionItemPeriod(now time.Time) (time.Time, time.Time) {
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	return end.AddDate(0, 0, -7), end
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
	RecentActionRuns []actionitems.RunSummary
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

type actionItemsData struct {
	AppName         string
	UserName        string
	LLMEnabled      bool
	LLMModel        string
	PeriodStartDate string
	PeriodEndDate   string
	Candidates      []actionitems.Candidate
	Previewed       bool
}

type briefingData struct {
	AppName string
	Run     actionitems.Run
}

type artifactData struct {
	AppName           string
	Artifact          artifacts.ArtifactDetail
	Warnings          []string
	Errors            []string
	MarkdownPreview   string
	MarkdownTruncated bool
	TextPreview       string
	TextTruncated     bool
	DocStatusClass    string
}

type jobData struct {
	AppName            string
	Job                jobs.Job
	Payload            ingest.FilePayload
	HasPayload         bool
	Result             ingest.JobResult
	HasResult          bool
	ArtifactID         string
	ArtifactExists     bool
	ResultDoclingClass string
}
