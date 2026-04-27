package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateAppliesInitialMigration(t *testing.T) {
	database := openTempDB(t)
	defer database.Close()

	if err := Migrate(context.Background(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var appName string
	if err := database.QueryRow(`SELECT value FROM app_metadata WHERE key = 'app_name'`).Scan(&appName); err != nil {
		t.Fatalf("read app metadata: %v", err)
	}
	if appName != "Zora" {
		t.Fatalf("app_name = %q", appName)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	database := openTempDB(t)
	defer database.Close()

	if err := Migrate(context.Background(), database); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := Migrate(context.Background(), database); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("version 1 migration count = %d", count)
	}
}

func TestMigrateRecordsAppliedVersion(t *testing.T) {
	database := openTempDB(t)
	defer database.Close()

	if err := Migrate(context.Background(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var name string
	if err := database.QueryRow(`SELECT name FROM schema_migrations WHERE version = 1`).Scan(&name); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if name != "app metadata" {
		t.Fatalf("migration name = %q", name)
	}
}

func TestMigrateCreatesIngestSchemaAndFTS(t *testing.T) {
	database := openTempDB(t)
	defer database.Close()

	if err := Migrate(context.Background(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, table := range []string{
		"blob",
		"artifact",
		"extracted_text",
		"search_document",
		"artifact_fts",
		"ingest_job",
		"watch_file_state",
		"extracted_document",
		"artifact_chunk",
		"artifact_chunk_fts",
	} {
		var name string
		if err := database.QueryRow(`
SELECT name FROM sqlite_master WHERE name = ?
UNION ALL
SELECT name FROM sqlite_master WHERE name = ?
`, table, table).Scan(&name); err != nil {
			t.Fatalf("expected table %s: %v", table, err)
		}
	}

	if _, err := database.Exec(`
INSERT INTO blob (hash, algorithm, size_bytes, storage_path, created_at)
VALUES ('abc', 'sha256', 3, '/tmp/abc', '2026-01-01T00:00:00Z');
INSERT INTO artifact (id, type, source, source_id, title, owner, content_hash, captured_at, event_at, created_at)
VALUES ('art_1', 'text', 'watch_folder', 'source_1', 'Invoice', 'florian', 'abc', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO search_document (artifact_id, title, text, updated_at)
VALUES ('art_1', 'Invoice', 'Payment is due next week.', '2026-01-01T00:00:00Z');
INSERT INTO artifact_chunk (id, artifact_id, ordinal, title, text, heading_path, char_start, char_end, created_at)
VALUES ('chk_1', 'art_1', 0, 'Invoice', 'Payment chunk for search.', 'Invoice', 0, 25, '2026-01-01T00:00:00Z');
`); err != nil {
		t.Fatalf("insert FTS fixture: %v", err)
	}

	var artifactID string
	if err := database.QueryRow(`
SELECT sd.artifact_id
FROM artifact_fts
JOIN search_document sd ON sd.rowid = artifact_fts.rowid
WHERE artifact_fts MATCH 'payment'
`).Scan(&artifactID); err != nil {
		t.Fatalf("query FTS: %v", err)
	}
	if artifactID != "art_1" {
		t.Fatalf("artifactID = %q", artifactID)
	}

	var chunkID string
	if err := database.QueryRow(`
SELECT c.id
FROM artifact_chunk_fts
JOIN artifact_chunk c ON c.rowid = artifact_chunk_fts.rowid
WHERE artifact_chunk_fts MATCH 'chunk'
`).Scan(&chunkID); err != nil {
		t.Fatalf("query chunk FTS: %v", err)
	}
	if chunkID != "chk_1" {
		t.Fatalf("chunkID = %q", chunkID)
	}
}

func openTempDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "main.sqlite")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open temp DB: %v", err)
	}
	return database
}
