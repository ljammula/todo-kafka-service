// buildgate-challenge: AC-2
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestChallengeVersionGETDocument(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	cases := []struct {
		name    string
		query   string
		body    string
		headers map[string]string
	}{
		{name: "empty"},
		{name: "empty and repeated query parameters", query: "?service=&service=other&unused="},
		{name: "encoded query parameters", query: "?name=%E2%9C%93%26%3D&done=invalid&limit=0"},
		{name: "ignored request body", body: "}{not-json", headers: map[string]string{"Content-Type": "application/json"}},
		{name: "unrelated headers", headers: map[string]string{
			"Accept":          "application/xml",
			"Idempotency-Key": "version-probe",
			"X-Request-ID":    "different-request",
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, server.URL+"/version"+testCase.query, strings.NewReader(testCase.body))
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range testCase.headers {
				request.Header.Set(name, value)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("GET /version: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.StatusCode)
			}
			if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", contentType)
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			challengeAssertVersionObject(t, body)
		})
	}
}

func TestChallengeVersionConcurrentResponses(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	type result struct {
		status      int
		contentType string
		body        []byte
		err         error
	}
	const requests = 32
	start := make(chan struct{})
	results := make(chan result, requests)
	for requestNumber := 0; requestNumber < requests; requestNumber++ {
		go func(number int) {
			<-start
			request, err := http.NewRequest(http.MethodGet, server.URL+"/version?request="+strconv.Itoa(number), nil)
			if err != nil {
				results <- result{err: err}
				return
			}
			request.Header.Set("X-Request-ID", strconv.Itoa(number))
			response, err := client.Do(request)
			if err != nil {
				results <- result{err: err}
				return
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			results <- result{status: response.StatusCode, contentType: response.Header.Get("Content-Type"), body: body, err: err}
		}(requestNumber)
	}
	close(start)
	for requestNumber := 0; requestNumber < requests; requestNumber++ {
		response := <-results
		if response.err != nil {
			t.Errorf("concurrent GET /version: %v", response.err)
			continue
		}
		if response.status != http.StatusOK || response.contentType != "application/json" {
			t.Errorf("concurrent GET /version = status %d, Content-Type %q", response.status, response.contentType)
			continue
		}
		challengeAssertVersionObject(t, response.body)
	}
}

func challengeAssertVersionObject(t *testing.T, body []byte) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		t.Fatalf("expected JSON object, got %q: %v", body, err)
	}
	key, err := decoder.Token()
	if err != nil || key != "service" {
		t.Fatalf("expected service field, got %q: %v", body, err)
	}
	value, err := decoder.Token()
	if err != nil || value != "todo-kafka-service" {
		t.Fatalf("expected service value todo-kafka-service, got %q: %v", body, err)
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		t.Fatalf("expected object with exactly one field, got %q: %v", body, err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		t.Fatalf("expected end of JSON document, got token %v and error %v in %q", token, err, body)
	}
}
