package server

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"zora/internal/config"
)

func TestThreadsListEmptyState(t *testing.T) {
	handler := newTestServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/threads", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "Threads") || !strings.Contains(body, "No threads yet") {
		t.Fatalf("threads body = %s", body)
	}
}

func TestThreadsListShowsActiveThread(t *testing.T) {
	handler, database := newTestServerWithConfig(t, config.Default())

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	insertServerArtifact(t, database, "art_v1", "pdf", "Verizon bill", now.AddDate(0, 0, -1), "Verizon Wireless statement.")
	insertTestThread(t, database, "thr_test", "vendor_account", "Bill · Verizon · Apr 2026", now)
	insertTestThreadMember(t, database, "thr_test", "art_v1", now)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/threads", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Verizon") || !strings.Contains(recorder.Body.String(), "Vendor accounts") {
		t.Fatalf("body = %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/threads/thr_test", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "Verizon bill") || !strings.Contains(body, "Timeline") {
		t.Fatalf("detail body = %s", body)
	}
}

func TestThreadDetailNotFoundRendersNotFound(t *testing.T) {
	handler := newTestServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/threads/missing", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "no longer exists") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func insertTestThread(t *testing.T, database *sql.DB, id, kind, title string, now time.Time) {
	t.Helper()
	if _, err := database.Exec(`
INSERT INTO thread (id, kind, title, summary, date_start, date_end, status, signature_json, created_at, updated_at)
VALUES (?, ?, ?, NULL, ?, ?, 'active', '{}', ?, ?)
`,
		id, kind, title,
		now.UTC().Format(time.RFC3339Nano),
		now.UTC().Format(time.RFC3339Nano),
		now.UTC().Format(time.RFC3339Nano),
		now.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
}

func insertTestThreadMember(t *testing.T, database *sql.DB, threadID, artifactID string, now time.Time) {
	t.Helper()
	if _, err := database.Exec(`
INSERT INTO thread_member (thread_id, artifact_id, score, source, added_at)
VALUES (?, ?, ?, 'rule', ?)
`, threadID, artifactID, 1.0, now.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert thread member: %v", err)
	}
}
