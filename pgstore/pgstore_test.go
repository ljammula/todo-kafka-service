//go:build integration

package pgstore

import (
	"os"
	"testing"
)

// These tests require the docker-compose Postgres service to be up
// (`docker compose up -d postgres`) and TODO_DATABASE_URL set to reach it
// -- see the repo's own Makefile "verify" target. Excluded from the
// default `go test ./...` run (no "integration" build tag) so nothing
// else needs Docker.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TODO_DATABASE_URL")
	if dsn == "" {
		t.Skip("TODO_DATABASE_URL not set; run via `make verify`")
	}
	s, err := New(dsn)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.db.Exec(`TRUNCATE todos`); err != nil {
		t.Fatalf("reset todos table: %v", err)
	}
	return s
}

func TestAddWithIDAndGet(t *testing.T) {
	s := openTestStore(t)

	if err := s.AddWithID("t1", "buy milk"); err != nil {
		t.Fatalf("AddWithID: %v", err)
	}

	got, ok, err := s.Get("t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || got.Title != "buy milk" {
		t.Fatalf("unexpected get result: ok=%v got=%+v", ok, got)
	}
}

func TestAddWithIDIsIdempotent(t *testing.T) {
	s := openTestStore(t)

	if err := s.AddWithID("t1", "first title"); err != nil {
		t.Fatalf("AddWithID (first): %v", err)
	}
	// Simulates Kafka redelivering the same message -- must not error and
	// must not overwrite the existing row.
	if err := s.AddWithID("t1", "second title"); err != nil {
		t.Fatalf("AddWithID (redelivery): %v", err)
	}

	got, ok, err := s.Get("t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || got.Title != "first title" {
		t.Fatalf("expected the first write to win, got ok=%v got=%+v", ok, got)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	s := openTestStore(t)

	if err := s.AddWithID("t1", "old title"); err != nil {
		t.Fatalf("AddWithID: %v", err)
	}

	updated, ok, err := s.Update("t1", "new title", true)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !ok || updated.Title != "new title" || !updated.Done {
		t.Fatalf("unexpected update result: ok=%v updated=%+v", ok, updated)
	}

	deleted, err := s.Delete("t1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !deleted {
		t.Fatal("expected Delete to report a row was removed")
	}

	_, ok, err = s.Get("t1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if ok {
		t.Fatal("expected todo to be gone after Delete")
	}
}

func TestListOrdering(t *testing.T) {
	s := openTestStore(t)

	if err := s.AddWithID("a", "a"); err != nil {
		t.Fatalf("AddWithID a: %v", err)
	}
	if err := s.AddWithID("b", "b"); err != nil {
		t.Fatalf("AddWithID b: %v", err)
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].Title != "a" || list[1].Title != "b" {
		t.Fatalf("unexpected list: %+v", list)
	}
}
