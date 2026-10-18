package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"todo-service/event"
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

func TestHealthz(t *testing.T) {
	tests := []struct {
		name   string
		method string
		status int
	}{
		{name: "GET", method: http.MethodGet, status: http.StatusOK},
		{name: "HEAD", method: http.MethodHead, status: http.StatusMethodNotAllowed},
		{name: "POST", method: http.MethodPost, status: http.StatusMethodNotAllowed},
		{name: "PUT", method: http.MethodPut, status: http.StatusMethodNotAllowed},
		{name: "PATCH", method: http.MethodPatch, status: http.StatusMethodNotAllowed},
		{name: "DELETE", method: http.MethodDelete, status: http.StatusMethodNotAllowed},
		{name: "OPTIONS", method: http.MethodOptions, status: http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handleHealthz(w, httptest.NewRequest(tt.method, "/healthz", nil))
			if w.Code != tt.status {
				t.Fatalf("expected status %d, got %d", tt.status, w.Code)
			}
			if tt.method != http.MethodGet {
				if got := w.Header().Get("Allow"); got != http.MethodGet {
					t.Fatalf("expected Allow: GET, got %q", got)
				}
				return
			}
			if got := w.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("expected application/json, got %q", got)
			}
			var body map[string]string
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(body) != 1 || body["status"] != "ok" {
				t.Fatalf("unexpected response: %#v", body)
			}
		})
	}
}

func TestHealthzRoute(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 || body["status"] != "ok" {
		t.Fatalf("unexpected response: %#v", body)
	}
}

type recordingPublisher struct {
	events []event.TodoCreated
}

func (p *recordingPublisher) Publish(_ context.Context, evt event.TodoCreated) error {
	p.events = append(p.events, evt)
	return nil
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

func TestListDoneFilter(t *testing.T) {
	initial := []Todo{
		{ID: "one", Title: "first", Done: false},
		{ID: "two", Title: "second", Done: true},
		{ID: "three", Title: "third", Done: true},
		{ID: "four", Title: "fourth", Done: false},
	}
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantIDs    []string
		todos      []Todo
	}{
		{name: "unfiltered", wantStatus: http.StatusOK, wantIDs: []string{"one", "two", "three", "four"}},
		{name: "done true", query: "?done=true", wantStatus: http.StatusOK, wantIDs: []string{"two", "three"}},
		{name: "done false", query: "?done=false", wantStatus: http.StatusOK, wantIDs: []string{"one", "four"}},
		{name: "true no matches", query: "?done=true", wantStatus: http.StatusOK, wantIDs: []string{}, todos: []Todo{{ID: "one", Done: false}}},
		{name: "false no matches", query: "?done=false", wantStatus: http.StatusOK, wantIDs: []string{}, todos: []Todo{{ID: "one", Done: true}}},
		{name: "empty", query: "?done=", wantStatus: http.StatusBadRequest},
		{name: "case variant", query: "?done=True", wantStatus: http.StatusBadRequest},
		{name: "arbitrary", query: "?done=yes", wantStatus: http.StatusBadRequest},
		{name: "numeric", query: "?done=1", wantStatus: http.StatusBadRequest},
		{name: "repeated", query: "?done=true&done=false", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetStore()
			todos := initial
			if tt.todos != nil {
				todos = tt.todos
			}
			store.todos = append([]Todo(nil), todos...)
			before := store.List()
			w := httptest.NewRecorder()
			handleTodos(w, httptest.NewRequest(http.MethodGet, "/todos"+tt.query, nil))
			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, w.Code)
			}
			if tt.wantStatus == http.StatusBadRequest {
				if w.Body.Len() != 0 {
					t.Fatalf("expected empty response body, got %q", w.Body.String())
				}
				if got := store.List(); !reflect.DeepEqual(got, before) {
					t.Fatalf("store changed: got %+v, want %+v", got, before)
				}
				return
			}
			var got []Todo
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("got %d todos, want %d", len(got), len(tt.wantIDs))
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Fatalf("todo %d has ID %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}

	resetStore()
	store.todos = []Todo{{ID: "only", Done: false}}
	w := httptest.NewRecorder()
	handleTodos(w, httptest.NewRequest(http.MethodGet, "/todos?done=true", nil))
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("no-match response = %q, want []", got)
	}
}

func TestDoneFilterDoesNotAffectItemLookup(t *testing.T) {
	initial := Todo{ID: "wanted", Title: "keep me", Done: true}
	for _, query := range []string{"", "?done=true", "?done=false", "?done=invalid", "?done=true&done=false"} {
		t.Run(query, func(t *testing.T) {
			resetStore()
			store.todos = []Todo{initial}
			w := httptest.NewRecorder()
			handleTodos(w, httptest.NewRequest(http.MethodGet, "/todos/wanted"+query, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			var got Todo
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got != initial {
				t.Fatalf("got %+v, want %+v", got, initial)
			}
		})
	}
}

func TestListLimit(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantIDs    []string
	}{
		{name: "unbounded", wantStatus: http.StatusOK, wantIDs: []string{"one", "two", "three"}},
		{name: "one", query: "?limit=1", wantStatus: http.StatusOK, wantIDs: []string{"one"}},
		{name: "below length", query: "?limit=2", wantStatus: http.StatusOK, wantIDs: []string{"one", "two"}},
		{name: "equal length", query: "?limit=3", wantStatus: http.StatusOK, wantIDs: []string{"one", "two", "three"}},
		{name: "above length", query: "?limit=9", wantStatus: http.StatusOK, wantIDs: []string{"one", "two", "three"}},
		{name: "empty store", query: "?limit=1", wantStatus: http.StatusOK, wantIDs: []string{}},
		{name: "empty", query: "?limit=", wantStatus: http.StatusBadRequest},
		{name: "alphabetic", query: "?limit=abc", wantStatus: http.StatusBadRequest},
		{name: "decimal", query: "?limit=1.5", wantStatus: http.StatusBadRequest},
		{name: "zero", query: "?limit=0", wantStatus: http.StatusBadRequest},
		{name: "negative", query: "?limit=-1", wantStatus: http.StatusBadRequest},
		{name: "overflow", query: "?limit=999999999999999999999999999", wantStatus: http.StatusBadRequest},
		{name: "repeated", query: "?limit=1&limit=2", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetStore()
			if tt.name != "empty store" {
				store.todos = []Todo{{ID: "one"}, {ID: "two"}, {ID: "three"}}
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/todos"+tt.query, nil)
			handleTodos(w, r)
			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, w.Code)
			}
			if tt.wantStatus == http.StatusBadRequest {
				if w.Body.Len() != 0 {
					t.Fatalf("expected empty response body, got %q", w.Body.String())
				}
				return
			}
			var got []Todo
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("got %d todos, want %d", len(got), len(tt.wantIDs))
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Fatalf("todo %d has ID %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestListLimitDoesNotAffectItemLookup(t *testing.T) {
	tests := []string{"0", "not-a-number"}
	for _, limit := range tests {
		t.Run(limit, func(t *testing.T) {
			resetStore()
			store.todos = []Todo{{ID: "wanted", Title: "keep me"}}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/todos/wanted?limit="+limit, nil)
			handleTodos(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			var got Todo
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.ID != "wanted" {
				t.Fatalf("got todo %+v, want wanted", got)
			}
		})
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
	tests := []struct {
		name       string
		method     string
		body       string
		initial    Todo
		wantStatus int
		wantTodo   Todo
	}{
		{
			name:       "PUT omits title",
			method:     http.MethodPut,
			body:       `{"done":true}`,
			initial:    Todo{ID: "put-omitted", Title: "old"},
			wantStatus: http.StatusBadRequest,
			wantTodo:   Todo{ID: "put-omitted", Title: "old"},
		},
		{
			name:       "PUT empty title",
			method:     http.MethodPut,
			body:       `{"title":"","done":true}`,
			initial:    Todo{ID: "put-empty", Title: "old"},
			wantStatus: http.StatusBadRequest,
			wantTodo:   Todo{ID: "put-empty", Title: "old"},
		},
		{
			name:       "PATCH empty title",
			method:     http.MethodPatch,
			body:       `{"title":""}`,
			initial:    Todo{ID: "patch-empty", Title: "old", Done: true},
			wantStatus: http.StatusBadRequest,
			wantTodo:   Todo{ID: "patch-empty", Title: "old", Done: true},
		},
		{
			name:       "valid PUT",
			method:     http.MethodPut,
			body:       `{"title":"new title","done":true}`,
			initial:    Todo{ID: "put-valid", Title: "old"},
			wantStatus: http.StatusOK,
			wantTodo:   Todo{ID: "put-valid", Title: "new title", Done: true},
		},
		{
			name:       "valid PATCH preserves omitted fields",
			method:     http.MethodPatch,
			body:       `{"title":"new title"}`,
			initial:    Todo{ID: "patch-valid", Title: "old", Done: true},
			wantStatus: http.StatusOK,
			wantTodo:   Todo{ID: "patch-valid", Title: "new title", Done: true},
		},
		{
			name:       "done-only PATCH preserves title",
			method:     http.MethodPatch,
			body:       `{"done":true}`,
			initial:    Todo{ID: "patch-done-only", Title: "old"},
			wantStatus: http.StatusOK,
			wantTodo:   Todo{ID: "patch-done-only", Title: "old", Done: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetStore()
			store.todos = []Todo{tt.initial}

			w := handler("/"+tt.initial.ID, tt.method, tt.body)
			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, w.Code)
			}
			if tt.wantStatus == http.StatusBadRequest && w.Body.Len() != 0 {
				t.Fatalf("expected empty response body, got %q", w.Body.String())
			}
			if tt.wantStatus == http.StatusOK {
				var got Todo
				if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if got != tt.wantTodo {
					t.Fatalf("response todo = %+v, want %+v", got, tt.wantTodo)
				}
			}

			got, ok := store.Get(tt.initial.ID)
			if !ok {
				t.Fatal("todo was removed")
			}
			if got != tt.wantTodo {
				t.Fatalf("stored todo = %+v, want %+v", got, tt.wantTodo)
			}
		})
	}
}

func TestWhitespaceOnlyPostRejectedBeforePublishOrStore(t *testing.T) {
	previousPublisher := publisher
	defer func() { publisher = previousPublisher }()

	empty := handler("", http.MethodPost, `{"title":""}`)
	if empty.Code != http.StatusBadRequest || empty.Body.Len() != 0 {
		t.Fatalf("empty title: got status %d and body %q", empty.Code, empty.Body.String())
	}
	for _, title := range []string{"   ", "\t"} {
		t.Run(fmt.Sprintf("%q", title), func(t *testing.T) {
			resetStore()
			recorder := &recordingPublisher{}
			publisher = recorder
			requestBody, err := json.Marshal(struct {
				Title string `json:"title"`
			}{Title: title})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			w := handler("", http.MethodPost, string(requestBody))
			if w.Code != http.StatusBadRequest || w.Body.String() != empty.Body.String() {
				t.Fatalf("got status %d and body %q, want 400 and empty body", w.Code, w.Body.String())
			}
			if len(store.List()) != 0 {
				t.Fatal("whitespace-only POST created a todo")
			}
			if len(recorder.events) != 0 {
				t.Fatalf("publisher called %d times", len(recorder.events))
			}
		})
	}
}

func TestPostPreservesSurroundingWhitespace(t *testing.T) {
	previousPublisher := publisher
	defer func() { publisher = previousPublisher }()
	resetStore()
	recorder := &recordingPublisher{}
	publisher = recorder
	const title = "  visible\t"
	w := handler("", http.MethodPost, `{"title":"  visible\t"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var got Todo
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Title != title || store.List()[0].Title != title {
		t.Fatalf("title was not preserved: response %q, store %q", got.Title, store.List()[0].Title)
	}
	if len(recorder.events) != 1 || recorder.events[0].Title != title {
		t.Fatalf("published events = %+v, want title %q", recorder.events, title)
	}
}

func TestWhitespaceOnlyPutAndPatchDoNotMutate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		method  string
		body    string
		initial Todo
	}{
		{"PUT spaces", http.MethodPut, `{"title":"   ","done":false}`, Todo{ID: "put-spaces", Title: "before", Done: true}},
		{"PUT tab", http.MethodPut, `{"title":"\t","done":false}`, Todo{ID: "put-tab", Title: "before", Done: true}},
		{"PATCH spaces", http.MethodPatch, `{"title":"   ","done":false}`, Todo{ID: "patch-spaces", Title: "before", Done: true}},
		{"PATCH tab", http.MethodPatch, `{"title":"\t","done":false}`, Todo{ID: "patch-tab", Title: "before", Done: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetStore()
			store.todos = []Todo{tc.initial}
			w := handler("/"+tc.initial.ID, tc.method, tc.body)
			if w.Code != http.StatusBadRequest || w.Body.Len() != 0 {
				t.Fatalf("got status %d and body %q, want 400 and empty body", w.Code, w.Body.String())
			}
			got, _ := store.Get(tc.initial.ID)
			if got != tc.initial {
				t.Fatalf("stored todo = %+v, want unchanged %+v", got, tc.initial)
			}
		})
	}
}

func TestPutAndPatchPreserveSurroundingWhitespace(t *testing.T) {
	resetStore()
	store.todos = []Todo{{ID: "put-visible", Title: "old"}, {ID: "patch-visible", Title: "old", Done: true}}

	put := handler("/put-visible", http.MethodPut, `{"title":"  put visible  ","done":true}`)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d", put.Code)
	}
	var putTodo Todo
	if err := json.NewDecoder(put.Body).Decode(&putTodo); err != nil {
		t.Fatal(err)
	}
	if putTodo.Title != "  put visible  " || store.todos[0].Title != "  put visible  " {
		t.Fatalf("PUT title was trimmed: response %q, store %q", putTodo.Title, store.todos[0].Title)
	}

	patch := handler("/patch-visible", http.MethodPatch, `{"title":"\tpatch visible  ","done":false}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("PATCH expected 200, got %d", patch.Code)
	}
	var patchTodo Todo
	if err := json.NewDecoder(patch.Body).Decode(&patchTodo); err != nil {
		t.Fatal(err)
	}
	if patchTodo.Title != "\tpatch visible  " || store.todos[1].Title != "\tpatch visible  " || store.todos[1].Done {
		t.Fatalf("PATCH changed title or done unexpectedly: response %+v, store %+v", patchTodo, store.todos[1])
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

type countingDurable struct {
	calls int
}

func (f *countingDurable) List() ([]pgstore.Todo, error) {
	f.calls++
	return nil, nil
}

func durableRequest(method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	w := httptest.NewRecorder()
	handleTodosDurable(w, req)
	return w
}

func TestTodosDurableFilters(t *testing.T) {
	prev := durableStore
	durableStore = fakeDurable{todos: []pgstore.Todo{
		{ID: "first", Title: "open", Done: false},
		{ID: "second", Title: "closed", Done: true},
		{ID: "third", Title: "also closed", Done: true},
	}}
	defer func() { durableStore = prev }()

	for _, test := range []struct {
		query string
		want  []string
	}{
		{"", []string{"first", "second", "third"}},
		{"?done=true", []string{"second", "third"}},
		{"?done=false", []string{"first"}},
	} {
		w := durableRequest(http.MethodGet, "/todos/durable"+test.query)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", test.query, w.Code)
		}
		var got []pgstore.Todo
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("%s: decode: %v", test.query, err)
		}
		if len(got) != len(test.want) {
			t.Fatalf("%s: got %d todos, want %d: %+v", test.query, len(got), len(test.want), got)
		}
		for i, id := range test.want {
			if got[i].ID != id {
				t.Fatalf("%s: item %d is %q, want %q", test.query, i, got[i].ID, id)
			}
		}
	}
}

func TestTodosDurableEmptyFilterIsArray(t *testing.T) {
	prev := durableStore
	durableStore = fakeDurable{todos: []pgstore.Todo{{ID: "open"}}}
	defer func() { durableStore = prev }()

	w := durableRequest(http.MethodGet, "/todos/durable?done=true")
	if w.Code != http.StatusOK || w.Body.String() != "[]\n" {
		t.Fatalf("expected empty JSON array, got status %d body %q", w.Code, w.Body.String())
	}
}

func TestTodosDurableRejectsInvalidDoneWithoutReading(t *testing.T) {
	for _, query := range []string{"?done=", "?done=TRUE", "?done=1", "?done=bogus"} {
		lister := &countingDurable{}
		prev := durableStore
		durableStore = lister
		w := durableRequest(http.MethodGet, "/todos/durable"+query)
		durableStore = prev
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", query, w.Code)
		}
		if !strings.Contains(w.Body.String(), "done must be true or false") {
			t.Errorf("%s: unexpected error %q", query, w.Body.String())
		}
		if lister.calls != 0 {
			t.Errorf("%s: durable store was read %d times", query, lister.calls)
		}
	}
}

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

type countingStatsDurable struct {
	todos []pgstore.Todo
	calls *int
}

func (f countingStatsDurable) List() ([]pgstore.Todo, error) {
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
	durableStore = countingStatsDurable{calls: &calls}
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

func assertTodoSummary(t *testing.T, w *httptest.ResponseRecorder, total, done, open int) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json, got %q", got)
	}
	var got map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	want := map[string]int{"total": total, "done": done, "open": open}
	if len(got) != len(want) {
		t.Fatalf("summary has fields %#v, want exactly total, done, and open", got)
	}
	for key, expected := range want {
		raw, ok := got[key]
		if !ok {
			t.Fatalf("summary is missing %q: %#v", key, got)
		}
		var actual int
		if err := json.Unmarshal(raw, &actual); err != nil {
			t.Fatalf("summary field %q is not an integer: %v", key, err)
		}
		if actual != expected {
			t.Errorf("summary %s = %d, want %d", key, actual, expected)
		}
	}
}

func TestTodosSummaryRoute(t *testing.T) {
	defer resetStore()
	previousDurableStore := durableStore
	defer func() { durableStore = previousDurableStore }()
	calls := 0
	durableStore = countingStatsDurable{
		todos: []pgstore.Todo{{Done: true}, {Done: true}, {Done: true}},
		calls: &calls,
	}

	tests := []struct {
		name  string
		todos []Todo
		total int
		done  int
		open  int
	}{
		{name: "empty"},
		{
			name: "mixed",
			todos: []Todo{
				{ID: "open-one", Title: "open"},
				{ID: "done-one", Title: "done", Done: true},
				{ID: "open-two", Title: "also open"},
			},
			total: 3,
			done:  1,
			open:  2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetStore()
			for _, todo := range tt.todos {
				store.AddWithID(todo.ID, todo.Title)
				if todo.Done {
					store.Update(todo.ID, todo.Title, true)
				}
			}
			mux := http.NewServeMux()
			registerRoutes(mux)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/todos/summary", nil))
			assertTodoSummary(t, w, tt.total, tt.done, tt.open)
		})
	}
	if calls != 0 {
		t.Fatalf("summary read the durable store %d times", calls)
	}
}

func TestTodosSummaryTracksLocalMutations(t *testing.T) {
	resetStore()
	defer resetStore()
	mux := http.NewServeMux()
	registerRoutes(mux)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	create := func(title string) Todo {
		w := request(http.MethodPost, "/todos", `{"title":"`+title+`"}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("create returned %d, want 201", w.Code)
		}
		var todo Todo
		if err := json.NewDecoder(w.Body).Decode(&todo); err != nil {
			t.Fatalf("decode created todo: %v", err)
		}
		return todo
	}
	assertCounts := func(total, done, open int) {
		assertTodoSummary(t, request(http.MethodGet, "/todos/summary", ""), total, done, open)
	}

	first := create("first")
	second := create("second")
	assertCounts(2, 0, 2)

	if w := request(http.MethodPatch, "/todos/"+second.ID, `{"done":true}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH returned %d, want 200", w.Code)
	}
	assertCounts(2, 1, 1)

	if w := request(http.MethodPut, "/todos/"+first.ID, `{"title":"first updated","done":true}`); w.Code != http.StatusOK {
		t.Fatalf("PUT returned %d, want 200", w.Code)
	}
	assertCounts(2, 2, 0)

	if w := request(http.MethodPatch, "/todos/"+second.ID, `{"done":false}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH returned %d, want 200", w.Code)
	}
	assertCounts(2, 1, 1)

	if w := request(http.MethodDelete, "/todos/"+second.ID, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE returned %d, want 204", w.Code)
	}
	assertCounts(1, 1, 0)
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
