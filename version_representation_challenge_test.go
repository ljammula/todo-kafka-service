// buildgate-challenge: AC-2 subject: main.go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func checkChallengeVersionDocument(t *testing.T, body io.Reader) {
	t.Helper()
	decoder := json.NewDecoder(body)
	open, err := decoder.Token()
	if err != nil || open != json.Delim('{') {
		t.Fatalf("expected JSON object, got %v (error %v)", open, err)
	}
	if !decoder.More() {
		t.Fatal("version object has no service field")
	}
	key, err := decoder.Token()
	if err != nil || key != "service" {
		t.Fatalf("expected service field, got %v (error %v)", key, err)
	}
	var service string
	if err := decoder.Decode(&service); err != nil {
		t.Fatalf("decode service: %v", err)
	}
	if service != "todo-kafka-service" {
		t.Errorf("service = %q, want todo-kafka-service", service)
	}
	if decoder.More() {
		t.Fatal("version object contains more than one field")
	}
	closeToken, err := decoder.Token()
	if err != nil || closeToken != json.Delim('}') {
		t.Fatalf("expected end of object, got %v (error %v)", closeToken, err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		t.Fatalf("unexpected data after version object: %v (error %v)", token, err)
	}
}

func TestChallengeVersionGETRepresentation(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	tests := []struct {
		name    string
		target  string
		body    string
		headers map[string]string
	}{
		{name: "plain", target: "/version"},
		{name: "empty query", target: "/version?"},
		{name: "repeated encoded query", target: "/version?service=other&service=%E2%9C%93"},
		{name: "non-json accept", target: "/version", headers: map[string]string{"Accept": "text/plain"}},
		{name: "body and idempotency key", target: "/version", body: "not-json", headers: map[string]string{
			"Content-Type": "application/json", "Idempotency-Key": "some-key",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, server.URL+test.target, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range test.headers {
				request.Header.Set(key, value)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.StatusCode)
			}
			if got := response.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			checkChallengeVersionDocument(t, response.Body)
		})
	}
}
