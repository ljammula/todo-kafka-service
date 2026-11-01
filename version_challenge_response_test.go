// buildgate-challenge: AC-2
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestChallengeVersionResponseIsOneExactJSONDocument(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	tests := []struct {
		name    string
		query   string
		headers map[string]string
		body    string
	}{
		{name: "plain"},
		{name: "empty query", query: "?"},
		{name: "unrelated query", query: "?service=other&version=123&service=again"},
		{name: "unicode query", query: "?" + url.Values{"q": {"日本語 & ☃"}}.Encode()},
		{name: "accept html", headers: map[string]string{"Accept": "text/html"}},
		{name: "conditional", headers: map[string]string{"If-None-Match": "*", "If-Modified-Since": "Wed, 21 Oct 2015 07:28:00 GMT"}},
		{name: "business headers and body", headers: map[string]string{"Idempotency-Key": "example", "Content-Type": "application/json", "X-HTTP-Method-Override": "POST"}, body: `{"service":"wrong","title":"ignored"}`},
	}

	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, server.URL+"/version"+test.query, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Host = "shared-gateway.example"
			for key, value := range test.headers {
				req.Header.Set(key, value)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			contentTypes := resp.Header.Values("Content-Type")
			if len(contentTypes) != 1 || contentTypes[0] != "application/json" {
				t.Fatalf("Content-Type = %q, want exactly application/json", contentTypes)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			challengeAssertVersionDocument(t, body)
		})
	}
}

func challengeAssertVersionDocument(t *testing.T, body []byte) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		t.Fatalf("response is not a JSON object: %q (token %v, error %v)", body, token, err)
	}
	if !decoder.More() {
		t.Fatalf("response object is empty: %q", body)
	}
	if key, err := decoder.Token(); err != nil || key != "service" {
		t.Fatalf("first field = %v, error %v; want service", key, err)
	}
	var service string
	if err := decoder.Decode(&service); err != nil || service != "todo-kafka-service" {
		t.Fatalf("service = %q, error %v; want todo-kafka-service", service, err)
	}
	if decoder.More() {
		t.Fatalf("response has an extra or duplicate field: %q", body)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		t.Fatalf("invalid object end: token %v, error %v", token, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("extra data after JSON object: %q (error %v)", body, err)
	}
}
