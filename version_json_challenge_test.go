// buildgate-challenge: AC-2
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func assertVersionDocumentChallenge(testContext *testing.T, response *http.Response) {
	testContext.Helper()
	if response.StatusCode != http.StatusOK {
		testContext.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Values("Content-Type"); !reflect.DeepEqual(got, []string{"application/json"}) {
		testContext.Fatalf("Content-Type = %q, want application/json", got)
	}
	decoder := json.NewDecoder(response.Body)
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		testContext.Fatalf("object start = %v, %v", token, err)
	}
	if token, err := decoder.Token(); err != nil || token != "service" {
		testContext.Fatalf("object key = %v, %v, want service", token, err)
	}
	var service string
	if err := decoder.Decode(&service); err != nil || service != "todo-kafka-service" {
		testContext.Fatalf("service = %q, decode error = %v", service, err)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		testContext.Fatalf("object end = %v, %v; unexpected or duplicate fields", token, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		testContext.Fatalf("response contains trailing JSON or invalid bytes: %v, %v", trailing, err)
	}
}

func TestVersionJSONRequestVariationsChallenge(testContext *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	testContext.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	tests := []struct {
		name    string
		target  string
		body    string
		headers http.Header
	}{
		{name: "plain", target: "/version"},
		{name: "empty-query", target: "/version?"},
		{name: "unrelated-parameters", target: "/version?done=invalid&limit=0&service=other&service="},
		{name: "encoded-query", target: "/version?name=%E2%98%83%26%3D&empty=&encoded=%00"},
		{name: "request-body", target: "/version", body: `{"service":"other"}`, headers: http.Header{"Content-Type": {"application/json"}}},
		{name: "accept-and-range", target: "/version", headers: http.Header{"Accept": {"text/plain"}, "Range": {"bytes=0-1"}}},
		{name: "conditional", target: "/version", headers: http.Header{"If-None-Match": {"*"}, "If-Modified-Since": {"Wed, 21 Oct 2037 07:28:00 GMT"}}},
		{name: "method-override", target: "/version", headers: http.Header{"X-Http-Method-Override": {"POST"}}},
	}
	for _, test := range tests {
		testContext.Run(test.name, func(testContext *testing.T) {
			request, err := http.NewRequest(http.MethodGet, server.URL+test.target, strings.NewReader(test.body))
			if err != nil {
				testContext.Fatal(err)
			}
			request.Header = test.headers.Clone()
			response, err := client.Do(request)
			if err != nil {
				testContext.Fatal(err)
			}
			defer response.Body.Close()
			assertVersionDocumentChallenge(testContext, response)
		})
	}
}

func TestVersionConcurrentResponsesChallenge(testContext *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	testContext.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	for requestNumber := 0; requestNumber < 24; requestNumber++ {
		testContext.Run("request", func(testContext *testing.T) {
			testContext.Parallel()
			response, err := client.Get(server.URL + "/version")
			if err != nil {
				testContext.Fatal(err)
			}
			defer response.Body.Close()
			assertVersionDocumentChallenge(testContext, response)
		})
	}
}
