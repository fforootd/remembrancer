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

func openTempDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "main.sqlite")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open temp DB: %v", err)
	}
	return database
}
