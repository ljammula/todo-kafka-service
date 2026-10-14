//go:build integration

package dbmigrate

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

func migrationTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TODO_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TODO_DATABASE_URL not set; run via `make verify-integration`")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	t.Cleanup(func() { admin.Close() })
	name := fmt.Sprintf("dbmigrate_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("create scratch database: %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TODO_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	t.Cleanup(func() { admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`) })
	return u.String()
}

func TestApplyUpAndDown(t *testing.T) {
	dsn := migrationTestDSN(t)
	if err := Apply(dsn, "up"); err != nil {
		t.Fatalf("Apply up: %v", err)
	}
	if err := Apply(dsn, "up"); err != nil {
		t.Fatalf("repeat Apply up: %v", err)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open scratch database: %v", err)
	}
	defer db.Close()
	var version int
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM golang_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != 2 || dirty {
		t.Fatalf("migration state: version=%d dirty=%v, want version 2 and clean", version, dirty)
	}
	if err := db.QueryRow(`SELECT to_regclass('todos') IS NOT NULL`).Scan(&dirty); err != nil || !dirty {
		t.Fatalf("todos table after up: exists=%v err=%v", dirty, err)
	}
	var legacyVersion int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&legacyVersion); err != nil || legacyVersion != 2 {
		t.Fatalf("legacy ledger after no-op up: version=%d err=%v, want 2", legacyVersion, err)
	}
	if err := Apply(dsn, "down"); err != nil {
		t.Fatalf("Apply down: %v", err)
	}
	if err := db.QueryRow(`SELECT to_regclass('todos') IS NULL`).Scan(&dirty); err != nil || !dirty {
		t.Fatalf("todos table after down: absent=%v err=%v", dirty, err)
	}
	var legacyRows int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&legacyRows); err != nil || legacyRows != 0 {
		t.Fatalf("legacy ledger after down: rows=%d err=%v, want empty", legacyRows, err)
	}
	if err := Apply(dsn, "down"); err != nil {
		t.Fatalf("repeat Apply down: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&legacyRows); err != nil || legacyRows != 0 {
		t.Fatalf("legacy ledger after no-op down: rows=%d err=%v, want empty", legacyRows, err)
	}
}

func TestApplyUpAdoptsExistingSchemaAndKeepsRows(t *testing.T) {
	dsn := migrationTestDSN(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open scratch database: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
INSERT INTO schema_migrations (version, name) VALUES (1, '001_create_todos.sql'), (2, '002_add_todos_created_at.sql');
CREATE TABLE todos (id TEXT PRIMARY KEY, title TEXT NOT NULL, done BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX todos_created_at_id_idx ON todos (created_at, id);
INSERT INTO todos (id, title, done) VALUES ('legacy', 'keep me', true)`)
	if err != nil {
		t.Fatalf("create pre-existing schema: %v", err)
	}
	if err := Apply(dsn, "up"); err != nil {
		t.Fatalf("Apply up to existing schema: %v", err)
	}
	var title string
	var done bool
	if err := db.QueryRow(`SELECT title, done FROM todos WHERE id='legacy'`).Scan(&title, &done); err != nil {
		t.Fatalf("read existing row: %v", err)
	}
	if title != "keep me" || !done {
		t.Fatalf("existing row changed: title=%q done=%v", title, done)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM golang_migrations`).Scan(&version); err != nil || version != 2 {
		t.Fatalf("new migration state: version=%d err=%v, want 2", version, err)
	}
	var legacyVersion int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&legacyVersion); err != nil || legacyVersion != 2 {
		t.Fatalf("legacy migration state: version=%d err=%v, want 2", legacyVersion, err)
	}
}

func TestApplyRejectsAndPreservesNewerLegacySchema(t *testing.T) {
	dsn := migrationTestDSN(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open scratch database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now()); INSERT INTO schema_migrations (version, name) VALUES (3, 'future')`); err != nil {
		t.Fatalf("create future legacy ledger: %v", err)
	}
	if err := Apply(dsn, "up"); err == nil {
		t.Fatal("Apply accepted a database with a newer legacy schema version")
	}
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 3 {
		t.Fatalf("future legacy version changed: version=%d err=%v", version, err)
	}
}
