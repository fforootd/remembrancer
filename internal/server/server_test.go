package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"remembrancer/internal/config"
	"remembrancer/internal/db"
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
	if !strings.Contains(recorder.Body.String(), "Remembrancer") {
		t.Fatalf("index body should include app name, got %q", recorder.Body.String())
	}
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()

	root := t.TempDir()
	cfg := config.Default()
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

	handler, err := New(cfg, database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New server: %v", err)
	}
	return handler
}
