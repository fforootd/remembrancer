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
		"briefing",
		"briefing_item",
		"briefing_item_artifact",
		"pipeline_stage",
		"evidence",
		"artifact_classification",
		"extracted_fact",
		"proposal",
		"artifact_relation",
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

func TestMigrateCreatesAssistantPipelineSchema(t *testing.T) {
	database := openTempDB(t)
	defer database.Close()

	if err := Migrate(context.Background(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if _, err := database.Exec(`
INSERT INTO blob (hash, algorithm, size_bytes, storage_path, created_at)
VALUES ('hash_pipeline', 'sha256', 3, '/tmp/hash_pipeline', '2026-01-01T00:00:00Z');
INSERT INTO artifact (id, type, source, source_id, title, owner, content_hash, captured_at, event_at, created_at)
VALUES ('art_pipeline', 'text', 'watch_folder', 'source_pipeline', 'School form', 'florian', 'hash_pipeline', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO pipeline_stage (artifact_id, stage, status, input_hash, created_at, updated_at)
VALUES ('art_pipeline', 'classify_artifact', 'succeeded', 'abc', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO evidence (id, artifact_id, kind, quote, char_start, char_end, provenance_json, created_at)
VALUES ('ev_pipeline', 'art_pipeline', 'extracted_text', 'Please return the school form.', 0, 30, '{}', '2026-01-01T00:00:00Z');
INSERT INTO artifact_classification (artifact_id, class, evidence_id, confidence, source_type, input_hash, created_at, updated_at)
VALUES ('art_pipeline', 'school_family', 'ev_pipeline', 0.9, 'rule', 'abc', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO extracted_fact (id, artifact_id, fact_type, value_json, text_value, evidence_id, quote, confidence, source_type, input_hash, created_at, updated_at)
VALUES ('fact_pipeline', 'art_pipeline', 'requested_action', '{"text":"return the school form"}', 'return the school form', 'ev_pipeline', 'Please return the school form.', 0.8, 'rule', 'abc', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO extracted_fact (id, artifact_id, fact_type, value_json, text_value, evidence_id, quote, confidence, source_type, input_hash, created_at, updated_at)
VALUES ('fact_payment_pipeline', 'art_pipeline', 'payment_status', '{"payment_status":"payment_due"}', 'payment_due', 'ev_pipeline', 'Amount Due', 0.8, 'rule', 'abc', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO proposal (id, type, status, source_artifact_id, title, confidence, created_at, updated_at)
VALUES ('prop_pipeline', 'artifact_relation', 'proposed', 'art_pipeline', 'related', 0.7, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
`); err != nil {
		t.Fatalf("insert pipeline fixture: %v", err)
	}

	var class string
	if err := database.QueryRow(`SELECT class FROM artifact_classification WHERE artifact_id = 'art_pipeline'`).Scan(&class); err != nil {
		t.Fatalf("read classification: %v", err)
	}
	if class != "school_family" {
		t.Fatalf("class = %q", class)
	}
}

func TestMigrateCreatesBriefingActionItemSchema(t *testing.T) {
	database := openTempDB(t)
	defer database.Close()

	if err := Migrate(context.Background(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if _, err := database.Exec(`
INSERT INTO blob (hash, algorithm, size_bytes, storage_path, created_at)
VALUES ('hash_1', 'sha256', 3, '/tmp/hash_1', '2026-01-01T00:00:00Z');
INSERT INTO artifact (id, type, source, source_id, title, owner, content_hash, captured_at, event_at, created_at)
VALUES ('art_1', 'text', 'watch_folder', 'source_1', 'School form', 'florian', 'hash_1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO briefing (id, period_start, period_end, title, source_query_json, model_name, prompt_version, created_at)
VALUES ('brf_1', '2026-01-01T00:00:00Z', '2026-01-08T00:00:00Z', 'Action items: 2026-01-01 to 2026-01-08', '{}', 'gemma4:e2b-it-q4_K_M', 'action-items-v1', '2026-01-08T00:00:00Z');
INSERT INTO briefing_item (id, briefing_id, category, title, summary, action_text, source_status, sort_order, created_at)
VALUES ('bri_1', 'brf_1', 'needs_action', 'Return school form', 'A form needs to be returned.', 'Return the form.', 'verified', 0, '2026-01-08T00:00:00Z');
INSERT INTO briefing_item_artifact (briefing_item_id, artifact_id, snippet)
VALUES ('bri_1', 'art_1', 'Please return this form.');
`); err != nil {
		t.Fatalf("insert briefing fixture: %v", err)
	}

	var count int
	if err := database.QueryRow(`
SELECT COUNT(*)
FROM briefing_item_artifact
WHERE briefing_item_id = 'bri_1'
	AND artifact_id = 'art_1'
`).Scan(&count); err != nil {
		t.Fatalf("count briefing artifact links: %v", err)
	}
	if count != 1 {
		t.Fatalf("briefing artifact link count = %d", count)
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
