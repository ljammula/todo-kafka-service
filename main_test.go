package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
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
