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

func TestPatchTitleOnly(t *testing.T) {
	resetStore()
	w := handler("", "POST", `{"title":"old"}`)
	var todo Todo
	json.NewDecoder(w.Body).Decode(&todo)

	w2 := handler("/"+todo.ID, "PATCH", `{"title":"new title"}`)
	if w2.Code != 200 {
		t.Fatalf("PATCH expected 200, got %d", w2.Code)
	}
	var updated Todo
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated.Title != "new title" {
		t.Fatalf("expected new title, got %q", updated.Title)
	}
	if updated.Done {
		t.Fatal("done should have stayed false")
	}

	w3 := handler("/"+todo.ID, "PATCH", `{"done":true}`)
	if w3.Code != 200 {
		t.Fatalf("PATCH expected 200, got %d", w3.Code)
	}
	var updated2 Todo
	json.NewDecoder(w3.Body).Decode(&updated2)
	if !updated2.Done {
		t.Fatal("expected done=true after patch")
	}
	if updated2.Title != "new title" {
		t.Fatalf("title should have stayed %q, got %q", "new title", updated2.Title)
	}
}

func TestPatchNonexistent(t *testing.T) {
	resetStore()
	w := handler("/999", "PATCH", `{"title":"x"}`)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestPatchEmptyBody(t *testing.T) {
	resetStore()
	w := handler("", "POST", `{"title":"old"}`)
	var todo Todo
	json.NewDecoder(w.Body).Decode(&todo)

	w2 := handler("/"+todo.ID, "PATCH", `{}`)
	if w2.Code != 400 {
		t.Fatalf("PATCH {} expected 400, got %d", w2.Code)
	}

	w3 := handler("", "PATCH", `{"title":"x"}`)
	if w3.Code != 400 {
		t.Fatalf("PATCH empty path expected 400, got %d", w3.Code)
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

type countingDurable struct {
	todos []pgstore.Todo
	calls *int
}

func (f countingDurable) List() ([]pgstore.Todo, error) {
	(*f.calls)++
	return f.todos, nil
}

func TestTodosDurableStats(t *testing.T) {
	prev := durableStore
	defer func() { durableStore = prev }()

	tests := []struct {
		name  string
		todos []pgstore.Todo
		total int
		done  int
	}{
		{"mixed", []pgstore.Todo{{Done: true}, {Done: false}, {Done: true}}, 3, 2},
		{"empty", []pgstore.Todo{}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			durableStore = fakeDurable{todos: tt.todos}
			w := httptest.NewRecorder()
			handleTodosDurableStats(w, httptest.NewRequest(http.MethodGet, "/todos/durable/stats", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			var got map[string]int
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got["total"] != tt.total || got["done"] != tt.done {
				t.Fatalf("unexpected stats fields: %#v", got)
			}
		})
	}
}

func TestTodosDurableStatsFailures(t *testing.T) {
	prev := durableStore
	defer func() { durableStore = prev }()

	for _, tc := range []struct {
		name string
		fake DurableLister
		want int
	}{
		{"unavailable", nil, http.StatusServiceUnavailable},
		{"backend error", fakeDurable{err: errors.New("boom")}, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			durableStore = tc.fake
			w := httptest.NewRecorder()
			handleTodosDurableStats(w, httptest.NewRequest(http.MethodGet, "/todos/durable/stats", nil))
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, w.Code)
			}
		})
	}
}

func TestTodosDurableStatsMethodRejectedBeforeRead(t *testing.T) {
	prev := durableStore
	defer func() { durableStore = prev }()
	calls := 0
	durableStore = countingDurable{calls: &calls}
	w := httptest.NewRecorder()
	handleTodosDurableStats(w, httptest.NewRequest(http.MethodPost, "/todos/durable/stats", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
	if calls != 0 {
		t.Fatalf("non-GET performed %d durable reads", calls)
	}
}

func TestDurableRoutes(t *testing.T) {
	prev := durableStore
	defer func() { durableStore = prev }()
	durableStore = fakeDurable{todos: []pgstore.Todo{{ID: "1"}}}
	mux := http.NewServeMux()
	registerRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/todos/durable", nil))
	var todos []pgstore.Todo
	if w.Code != http.StatusOK || json.NewDecoder(w.Body).Decode(&todos) != nil || len(todos) != 1 {
		t.Fatalf("durable list route returned %d and %+v", w.Code, todos)
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
