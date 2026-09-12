package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"todo-service/pgstore"
)

func handler(path string, method string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/todos"+path, bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	handleTodos(w, req)
	return w
}

func resetStore() {
	store = NewStore()
}

func TestListEmpty(t *testing.T) {
	resetStore()
	w := handler("", "GET", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var todos []Todo
	json.NewDecoder(w.Body).Decode(&todos)
	if len(todos) != 0 {
		t.Fatalf("expected empty list, got %d items", len(todos))
	}
}

func TestAddAndGet(t *testing.T) {
	resetStore()
	w := handler("", "POST", `{"title":"buy milk"}`)
	if w.Code != 201 {
		t.Fatalf("POST expected 201, got %d", w.Code)
	}
	var todo Todo
	json.NewDecoder(w.Body).Decode(&todo)
	if todo.Title != "buy milk" {
		t.Fatal("wrong title")
	}
	if todo.ID == "" {
		t.Fatal("missing id")
	}

	w2 := handler("/"+todo.ID, "GET", "")
	if w2.Code != 200 {
		t.Fatalf("GET expected 200, got %d", w2.Code)
	}
	var got Todo
	json.NewDecoder(w2.Body).Decode(&got)
	if got.Title != "buy milk" {
		t.Fatal("wrong title on GET")
	}
}

func TestUpdate(t *testing.T) {
	resetStore()
	w := handler("", "POST", `{"title":"old"}`)
	var todo Todo
	json.NewDecoder(w.Body).Decode(&todo)

	w2 := handler("/"+todo.ID, "PUT", `{"title":"new","done":true}`)
	if w2.Code != 200 {
		t.Fatalf("PUT expected 200, got %d", w2.Code)
	}
	var updated Todo
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated.Title != "new" || !updated.Done {
		t.Fatal("update did not apply")
	}
}

func TestDelete(t *testing.T) {
	resetStore()
	w := handler("", "POST", `{"title":"gone"}`)
	var todo Todo
	json.NewDecoder(w.Body).Decode(&todo)

	w2 := handler("/"+todo.ID, "DELETE", "")
	if w2.Code != 204 {
		t.Fatalf("DELETE expected 204, got %d", w2.Code)
	}

	w3 := handler("/"+todo.ID, "GET", "")
	if w3.Code != 404 {
		t.Fatalf("GET after delete expected 404, got %d", w3.Code)
	}
}

func TestNotFound(t *testing.T) {
	resetStore()
	w := handler("/999", "GET", "")
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestBadPost(t *testing.T) {
	resetStore()
	w := handler("", "POST", `{"title":""}`)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// fakeDurable is a DurableLister for unit tests -- no live Postgres.
type fakeDurable struct {
	todos []pgstore.Todo
	err   error
}

func (f fakeDurable) List() ([]pgstore.Todo, error) { return f.todos, f.err }

func TestTodosDurableList(t *testing.T) {
	resetStore()
	prev := durableStore
	durableStore = fakeDurable{todos: []pgstore.Todo{{ID: "1", Title: "a"}, {ID: "2", Title: "b", Done: true}}}
	defer func() { durableStore = prev }()

	req := httptest.NewRequest(http.MethodGet, "/todos/durable", nil)
	w := httptest.NewRecorder()
	handleTodosDurable(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var todos []pgstore.Todo
	if err := json.NewDecoder(w.Body).Decode(&todos); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(todos) != 2 || todos[0].ID != "1" || todos[0].Title != "a" || todos[1].ID != "2" || todos[1].Title != "b" || !todos[1].Done {
		t.Fatalf("unexpected todos: %+v", todos)
	}
}

func TestTodosDurableNilStore(t *testing.T) {
	prev := durableStore
	durableStore = nil
	defer func() { durableStore = prev }()

	req := httptest.NewRequest(http.MethodGet, "/todos/durable", nil)
	w := httptest.NewRecorder()
	handleTodosDurable(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestTodosDurableListError(t *testing.T) {
	prev := durableStore
	durableStore = fakeDurable{err: errors.New("boom")}
	defer func() { durableStore = prev }()

	req := httptest.NewRequest(http.MethodGet, "/todos/durable", nil)
	w := httptest.NewRecorder()
	handleTodosDurable(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestTodosDurableMethodNotAllowed(t *testing.T) {
	prev := durableStore
	durableStore = nil
	defer func() { durableStore = prev }()

	req := httptest.NewRequest(http.MethodPost, "/todos/durable", nil)
	w := httptest.NewRecorder()
	handleTodosDurable(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestListAfterAdd(t *testing.T) {
	resetStore()
	handler("", "POST", `{"title":"a"}`)
	handler("", "POST", `{"title":"b"}`)
	w := handler("", "GET", "")
	var todos []Todo
	json.NewDecoder(w.Body).Decode(&todos)
	if len(todos) != 2 {
		t.Fatalf("expected 2, got %d", len(todos))
	}
}
