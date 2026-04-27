package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Migration struct {
	Version int
	Name    string
	SQL     string
}

var migrations = []Migration{
	{
		Version: 1,
		Name:    "app metadata",
		SQL: `
CREATE TABLE app_metadata (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

INSERT INTO app_metadata (key, value, updated_at)
VALUES
	('app_name', 'Zora', datetime('now')),
	('milestone', '0', datetime('now'));
`,
	},
	{
		Version: 2,
		Name:    "queue backed ingest",
		SQL: `
CREATE TABLE blob (
	hash TEXT PRIMARY KEY,
	algorithm TEXT NOT NULL,
	size_bytes INTEGER NOT NULL,
	mime_type TEXT,
	storage_path TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE artifact (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL,
	source TEXT NOT NULL,
	source_id TEXT,
	title TEXT,
	owner TEXT NOT NULL,
	content_hash TEXT NOT NULL,
	captured_at TEXT NOT NULL,
	event_at TEXT,
	metadata_json TEXT,
	created_at TEXT NOT NULL,
	deleted_at TEXT,
	FOREIGN KEY (content_hash) REFERENCES blob(hash)
);

CREATE UNIQUE INDEX artifact_source_source_id_idx
ON artifact(source, source_id)
WHERE source_id IS NOT NULL;

CREATE INDEX artifact_event_at_idx ON artifact(event_at);

CREATE TABLE extracted_text (
	artifact_id TEXT PRIMARY KEY,
	text TEXT NOT NULL,
	extractor TEXT NOT NULL,
	extractor_version TEXT,
	created_at TEXT NOT NULL,
	FOREIGN KEY (artifact_id) REFERENCES artifact(id)
);

CREATE TABLE search_document (
	rowid INTEGER PRIMARY KEY,
	artifact_id TEXT NOT NULL UNIQUE,
	title TEXT,
	text TEXT,
	updated_at TEXT NOT NULL,
	FOREIGN KEY (artifact_id) REFERENCES artifact(id)
);

CREATE VIRTUAL TABLE artifact_fts USING fts5(
	title,
	text,
	content='search_document',
	content_rowid='rowid',
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER search_document_ai AFTER INSERT ON search_document BEGIN
	INSERT INTO artifact_fts(rowid, title, text)
	VALUES (new.rowid, new.title, new.text);
END;

CREATE TRIGGER search_document_ad AFTER DELETE ON search_document BEGIN
	INSERT INTO artifact_fts(artifact_fts, rowid, title, text)
	VALUES('delete', old.rowid, old.title, old.text);
END;

CREATE TRIGGER search_document_au AFTER UPDATE ON search_document BEGIN
	INSERT INTO artifact_fts(artifact_fts, rowid, title, text)
	VALUES('delete', old.rowid, old.title, old.text);
	INSERT INTO artifact_fts(rowid, title, text)
	VALUES (new.rowid, new.title, new.text);
END;

CREATE TABLE ingest_job (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'dead', 'cancelled')),
	priority INTEGER NOT NULL DEFAULT 0,
	payload_json TEXT NOT NULL,
	result_json TEXT,
	attempts INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL,
	run_after TEXT NOT NULL,
	locked_by TEXT,
	locked_at TEXT,
	last_error TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	finished_at TEXT,
	dedupe_key TEXT
);

CREATE INDEX ingest_job_claim_idx
ON ingest_job(status, run_after, priority DESC, created_at);

CREATE INDEX ingest_job_kind_status_idx
ON ingest_job(kind, status, updated_at);

CREATE UNIQUE INDEX ingest_job_active_dedupe_idx
ON ingest_job(kind, dedupe_key)
WHERE dedupe_key IS NOT NULL
	AND status IN ('queued', 'running', 'failed', 'succeeded');

CREATE TABLE watch_file_state (
	path TEXT PRIMARY KEY,
	size_bytes INTEGER NOT NULL,
	mtime TEXT NOT NULL,
	content_hash TEXT,
	source_id TEXT,
	last_seen_at TEXT NOT NULL,
	last_enqueued_job_id TEXT,
	ignored_reason TEXT,
	updated_at TEXT NOT NULL,
	FOREIGN KEY (last_enqueued_job_id) REFERENCES ingest_job(id)
);
`,
	},
	{
		Version: 3,
		Name:    "docling documents and chunks",
		SQL: `
CREATE TABLE extracted_document (
	artifact_id TEXT PRIMARY KEY,
	markdown TEXT,
	structured_json TEXT,
	metadata_json TEXT,
	status TEXT NOT NULL,
	extractor TEXT NOT NULL,
	extractor_version TEXT,
	processing_time_ms INTEGER NOT NULL DEFAULT 0,
	warnings_json TEXT,
	errors_json TEXT,
	created_at TEXT NOT NULL,
	FOREIGN KEY (artifact_id) REFERENCES artifact(id)
);

CREATE TABLE artifact_chunk (
	id TEXT PRIMARY KEY,
	artifact_id TEXT NOT NULL,
	ordinal INTEGER NOT NULL,
	title TEXT,
	text TEXT NOT NULL,
	heading_path TEXT,
	page_start INTEGER,
	page_end INTEGER,
	char_start INTEGER NOT NULL,
	char_end INTEGER NOT NULL,
	metadata_json TEXT,
	created_at TEXT NOT NULL,
	UNIQUE (artifact_id, ordinal),
	FOREIGN KEY (artifact_id) REFERENCES artifact(id)
);

CREATE INDEX artifact_chunk_artifact_idx
ON artifact_chunk(artifact_id, ordinal);

CREATE VIRTUAL TABLE artifact_chunk_fts USING fts5(
	title,
	heading_path,
	text,
	content='artifact_chunk',
	content_rowid='rowid',
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER artifact_chunk_ai AFTER INSERT ON artifact_chunk BEGIN
	INSERT INTO artifact_chunk_fts(rowid, title, heading_path, text)
	VALUES (new.rowid, new.title, new.heading_path, new.text);
END;

CREATE TRIGGER artifact_chunk_ad AFTER DELETE ON artifact_chunk BEGIN
	INSERT INTO artifact_chunk_fts(artifact_chunk_fts, rowid, title, heading_path, text)
	VALUES('delete', old.rowid, old.title, old.heading_path, old.text);
END;

CREATE TRIGGER artifact_chunk_au AFTER UPDATE ON artifact_chunk BEGIN
	INSERT INTO artifact_chunk_fts(artifact_chunk_fts, rowid, title, heading_path, text)
	VALUES('delete', old.rowid, old.title, old.heading_path, old.text);
	INSERT INTO artifact_chunk_fts(rowid, title, heading_path, text)
	VALUES (new.rowid, new.title, new.heading_path, new.text);
END;
`,
	},
	{
		Version: 4,
		Name:    "briefing action items",
		SQL: `
CREATE TABLE briefing (
	id TEXT PRIMARY KEY,
	period_start TEXT NOT NULL,
	period_end TEXT NOT NULL,
	title TEXT NOT NULL,
	source_query_json TEXT NOT NULL,
	model_name TEXT,
	prompt_version TEXT,
	created_at TEXT NOT NULL
);

CREATE INDEX briefing_created_at_idx
ON briefing(created_at DESC);

CREATE TABLE briefing_item (
	id TEXT PRIMARY KEY,
	briefing_id TEXT NOT NULL,
	category TEXT NOT NULL,
	title TEXT NOT NULL,
	summary TEXT NOT NULL,
	why_it_matters TEXT,
	action_text TEXT,
	due_at TEXT,
	confidence REAL,
	source_status TEXT NOT NULL CHECK (source_status IN ('verified', 'unverified')),
	sort_order INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	FOREIGN KEY (briefing_id) REFERENCES briefing(id) ON DELETE CASCADE
);

CREATE INDEX briefing_item_briefing_idx
ON briefing_item(briefing_id, sort_order);

CREATE TABLE briefing_item_artifact (
	briefing_item_id TEXT NOT NULL,
	artifact_id TEXT NOT NULL,
	snippet TEXT,
	PRIMARY KEY (briefing_item_id, artifact_id),
	FOREIGN KEY (briefing_item_id) REFERENCES briefing_item(id) ON DELETE CASCADE,
	FOREIGN KEY (artifact_id) REFERENCES artifact(id)
);
`,
	},
}

func Migrate(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL
);
`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, database)
	if err != nil {
		return err
	}

	for _, migration := range migrations {
		if applied[migration.Version] {
			continue
		}
		if err := applyMigration(ctx, database, migration); err != nil {
			return err
		}
	}

	return nil
}

func appliedVersions(ctx context.Context, database *sql.DB) (map[int]bool, error) {
	rows, err := database.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, database *sql.DB, migration Migration) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", migration.Version, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("apply migration %d %q: %w", migration.Version, migration.Name, err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		migration.Version,
		migration.Name,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("record migration %d: %w", migration.Version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", migration.Version, err)
	}
	return nil
}
