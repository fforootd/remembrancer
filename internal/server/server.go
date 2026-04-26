package server

import (
	"database/sql"
	"embed"
	"html/template"
	"log/slog"
	"net/http"

	"zora/internal/config"
)

//go:embed templates/*.html
var templateFS embed.FS

type Server struct {
	cfg       config.Config
	database  *sql.DB
	logger    *slog.Logger
	templates *template.Template
	mux       *http.ServeMux
}

func New(cfg config.Config, database *sql.DB, logger *slog.Logger) (*Server, error) {
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
	data := indexData{
		AppName:     "Zora",
		UserName:    s.cfg.User.DisplayName,
		RuntimePath: s.cfg.Paths.Runtime,
		ArchivePath: s.cfg.Paths.Archive,
		InboxPath:   s.cfg.Paths.Inbox,
		SQLitePath:  s.cfg.SQLite.Path,
		LLMEnabled:  s.cfg.LLM.Enabled,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		s.logger.Error("render index", "error", err)
	}
}

type indexData struct {
	AppName     string
	UserName    string
	RuntimePath string
	ArchivePath string
	InboxPath   string
	SQLitePath  string
	LLMEnabled  bool
}
