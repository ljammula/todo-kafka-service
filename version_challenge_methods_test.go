// buildgate-challenge: AC-3
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChallengeVersionRejectsNonGETMethodsOnHTTPRoute(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	methods := []string{
		http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodTrace, http.MethodConnect,
		"get", "GeT", "GETT", "PROPFIND", "REPORT", "M-SEARCH", "PURGE", "!",
	}
	requests := []struct {
		name     string
		query    string
		body     string
		override bool
	}{
		{name: "plain"},
		{name: "override", query: "?_method=GET", body: "ignored", override: true},
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			for _, test := range requests {
				t.Run(test.name, func(t *testing.T) {
					req, err := http.NewRequest(method, server.URL+"/version"+test.query, strings.NewReader(test.body))
					if err != nil {
						t.Fatal(err)
					}
					if test.override {
						req.Header.Set("X-HTTP-Method-Override", "GET")
						req.Header.Set("Idempotency-Key", "ignored")
					}
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					if resp.StatusCode != http.StatusMethodNotAllowed {
						t.Errorf("%s status = %d, want 405", method, resp.StatusCode)
					}
					values := resp.Header.Values("Allow")
					if len(values) != 1 || values[0] != http.MethodGet {
						t.Errorf("%s Allow = %q, want GET", method, values)
					}
				})
			}
		})
	}
}
