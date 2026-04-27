package server

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zora/internal/actionitems"
	"zora/internal/config"
	"zora/internal/db"
)

func TestHealthz(t *testing.T) {
	handler := newTestServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Body.String() != "OK\n" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestIndexIncludesAppName(t *testing.T) {
	handler := newTestServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Zora") {
		t.Fatalf("index body should include app name, got %q", recorder.Body.String())
	}
}

func TestTodayEmptyState(t *testing.T) {
	handler := newTestServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "No briefing yet") || !strings.Contains(body, "Today") {
		t.Fatalf("today body = %s", body)
	}
}

func TestAdminRouteShowsRuntime(t *testing.T) {
	handler := newTestServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "System status") || !strings.Contains(body, "Ingest queue") {
		t.Fatalf("admin body = %s", body)
	}
}

func TestActionItemsGetRedirectsToBriefings(t *testing.T) {
	handler := newTestServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/action-items", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Location"); got != "/briefings" {
		t.Fatalf("Location = %q", got)
	}
}

func TestStaticPicoServed(t *testing.T) {
	handler := newTestServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/static/pico.min.css", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("content-type = %q", recorder.Header().Get("Content-Type"))
	}
}

func TestLibraryAliasRendersList(t *testing.T) {
	handler := newTestServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/library", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Library") {
		t.Fatalf("library body = %s", recorder.Body.String())
	}
}

func TestActionItemsPreviewWhenLLMDisabled(t *testing.T) {
	handler, database := newTestServerWithConfig(t, config.Default())
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	insertServerArtifact(t, database, "art_school", "pdf", "School form", start.Add(24*time.Hour), "Please return the school form by Friday.")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/action-items", strings.NewReader("period_start=2026-04-20&period_end=2026-04-27"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Candidates") || !strings.Contains(recorder.Body.String(), "School form") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM briefing`).Scan(&count); err != nil {
		t.Fatalf("count briefings: %v", err)
	}
	if count != 0 {
		t.Fatalf("briefing count = %d", count)
	}
}

func TestActionItemsGenerateWithFakeReasoner(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = true
	handler, database := newTestServerWithConfig(t, cfg, WithActionItemReasoner(fakeReasoner{
		response: actionitems.GeneratedResponse{Items: []actionitems.GeneratedItem{{
			Category:    "needs_action",
			Title:       "Return school form",
			Summary:     "A school form needs to be returned.",
			ActionText:  "Return the school form.",
			ArtifactIDs: []string{"art_school"},
			EvidenceSnippets: []actionitems.EvidenceSnippet{{
				ArtifactID: "art_school",
				Quote:      "Please return the school form by Friday.",
			}},
			Confidence: 0.8,
		}}},
	}))
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	insertServerArtifact(t, database, "art_school", "pdf", "School form", start.Add(24*time.Hour), "Please return the school form by Friday.")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/action-items", strings.NewReader("period_start=2026-04-20&period_end=2026-04-27"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	location := recorder.Header().Get("Location")
	if !strings.HasPrefix(location, "/briefings/") {
		t.Fatalf("Location = %q", location)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, location, nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("briefing status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Return school form") || !strings.Contains(recorder.Body.String(), "art_school") {
		t.Fatalf("briefing body = %s", recorder.Body.String())
	}
}

func TestActionItemsPageListsRecentRuns(t *testing.T) {
	handler, database := newTestServerWithConfig(t, config.Default())
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	_, err := actionitems.Repository{DB: database}.CreateRun(context.Background(), actionitems.CreateRunParams{
		PeriodStart:   start,
		PeriodEnd:     start.AddDate(0, 0, 7),
		ModelName:     "test-model",
		PromptVersion: actionitems.PromptVersion,
		Now: func() time.Time {
			return start.AddDate(0, 0, 7)
		},
	})
	if err != nil {
		t.Fatalf("create action item run: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/briefings", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Past briefings") ||
		!strings.Contains(recorder.Body.String(), "Action items: 2026-04-20 to 2026-04-27") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestActionItemsGenerateFailureRendersPageWithCandidates(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = true
	handler, database := newTestServerWithConfig(t, cfg, WithActionItemReasoner(fakeReasoner{
		err: errors.New("ollama timed out"),
	}))
	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	insertServerArtifact(t, database, "art_school", "pdf", "School form", start.Add(24*time.Hour), "Please return the school form by Friday.")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/action-items", strings.NewReader("period_start=2026-04-20&period_end=2026-04-27"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "Generation failed") ||
		!strings.Contains(body, "ollama timed out") ||
		!strings.Contains(body, "Candidates") ||
		!strings.Contains(body, "School form") {
		t.Fatalf("body = %s", body)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM briefing`).Scan(&count); err != nil {
		t.Fatalf("count briefings: %v", err)
	}
	if count != 0 {
		t.Fatalf("briefing count = %d", count)
	}
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	server, _ := newTestServerWithConfig(t, config.Default())
	return server
}

func newTestServerWithConfig(t *testing.T, cfg config.Config, options ...Option) (*Server, *sql.DB) {
	t.Helper()

	root := t.TempDir()
	cfg.Paths.Runtime = filepath.Join(root, "runtime")
	cfg.Paths.Archive = filepath.Join(root, "archive")
	cfg.Paths.Inbox = filepath.Join(root, "inbox")
	cfg.SQLite.Path = filepath.Join(root, "runtime", "users", "florian", "main.sqlite")
	if err := config.EnsureLocalPaths(cfg); err != nil {
		t.Fatalf("EnsureLocalPaths: %v", err)
	}

	database, err := db.Open(cfg.SQLite.Path)
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
	})
	if err := db.Migrate(context.Background(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	handler, err := New(cfg, database, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, options...)
	if err != nil {
		t.Fatalf("New server: %v", err)
	}
	return handler, database
}

type fakeReasoner struct {
	response actionitems.GeneratedResponse
	err      error
}

func (f fakeReasoner) ExtractActionItems(ctx context.Context, req actionitems.Request) (actionitems.GeneratedResponse, error) {
	return f.response, f.err
}

func insertServerArtifact(t *testing.T, database *sql.DB, id, artifactType, title string, eventAt time.Time, text string) {
	t.Helper()
	hash := "hash_" + id
	if _, err := database.Exec(`
INSERT INTO blob (hash, algorithm, size_bytes, storage_path, created_at)
VALUES (?, 'sha256', 3, '/tmp/blob', '2026-04-20T00:00:00Z')
`, hash); err != nil {
		t.Fatalf("insert server blob: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO artifact (id, type, source, source_id, title, owner, content_hash, captured_at, event_at, created_at)
VALUES (?, ?, 'watch_folder', ?, ?, 'florian', ?, ?, ?, ?)
`,
		id,
		artifactType,
		"source_"+id,
		title,
		hash,
		eventAt.UTC().Format(time.RFC3339Nano),
		eventAt.UTC().Format(time.RFC3339Nano),
		eventAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert server artifact: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO extracted_text (artifact_id, text, extractor, created_at)
VALUES (?, ?, 'test', ?)
`,
		id,
		text,
		eventAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert server extracted text: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO artifact_chunk (id, artifact_id, ordinal, title, text, char_start, char_end, created_at)
VALUES (?, ?, 0, ?, ?, 0, ?, ?)
`,
		"chk_"+id,
		id,
		title,
		text,
		len(text),
		eventAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert server artifact: %v", err)
	}
}
