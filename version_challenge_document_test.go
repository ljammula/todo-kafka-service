// buildgate-challenge: AC-2
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChallengeVersionDocumentAcrossRequests(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	cases := []struct {
		name    string
		target  string
		body    string
		headers map[string]string
	}{
		{name: "empty request", target: "/version"},
		{
			name:   "query values and encoded bytes",
			target: "/version?service=other&done=invalid&done=true&limit=0&value=%00%F0%9F%92%A1",
		},
		{
			name:   "conditional and negotiation headers",
			target: "/version?",
			headers: map[string]string{
				"Accept":                 "text/html",
				"If-None-Match":          "*",
				"If-Modified-Since":      "Wed, 21 Oct 2015 07:28:00 GMT",
				"Range":                  "bytes=0-1",
				"X-HTTP-Method-Override": "DELETE",
			},
		},
		{
			name:   "irrelevant request body",
			target: "/version",
			body:   "{not a JSON document}\x00",
			headers: map[string]string{
				"Content-Type":    "application/json",
				"Idempotency-Key": "challenge-version",
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, server.URL+test.target, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.StatusCode)
			}
			if values := response.Header.Values("Content-Type"); len(values) != 1 || values[0] != "application/json" {
				t.Fatalf("Content-Type = %q, want exactly application/json", values)
			}
			challengeCheckVersionDocument(t, response.Body)
		})
	}
}

func challengeCheckVersionDocument(t *testing.T, body io.Reader) {
	t.Helper()
	decoder := json.NewDecoder(body)
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		t.Fatalf("expected JSON object, got %v (%v)", opening, err)
	}
	key, err := decoder.Token()
	if err != nil || key != "service" {
		t.Fatalf("expected service field, got %v (%v)", key, err)
	}
	var service string
	if err := decoder.Decode(&service); err != nil || service != "todo-kafka-service" {
		t.Fatalf("service = %q, decode error = %v", service, err)
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		t.Fatalf("expected end of object without more fields, got %v (%v)", closing, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("expected one JSON document, got trailing value %v (error %v)", trailing, err)
	}
}
