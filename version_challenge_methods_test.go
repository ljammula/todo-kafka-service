// buildgate-challenge: AC-3
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChallengeVersionRejectsEveryOtherMethodThroughServer(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	methods := []string{
		"HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT",
		"PROPFIND", "REPORT", "MKCOL", "M-SEARCH", "get", "Get", "GE%54",
		strings.Repeat("X", 128),
	}
	targets := []struct {
		name string
		path string
	}{
		{name: "exact", path: "/version"},
		{name: "query", path: "/version?method=GET"},
	}
	for _, method := range methods {
		for _, target := range targets {
			t.Run(method+"/"+target.name, func(t *testing.T) {
				request, err := http.NewRequest(method, server.URL+target.path, strings.NewReader("ignored"))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("X-HTTP-Method-Override", "GET")
				request.Header.Set("Access-Control-Request-Method", "GET")
				response, err := client.Do(request)
				if err != nil {
					t.Fatalf("%s request failed: %v", method, err)
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusMethodNotAllowed {
					t.Errorf("%s status = %d, want 405", method, response.StatusCode)
				}
				if values := response.Header.Values("Allow"); len(values) != 1 || values[0] != "GET" {
					t.Errorf("%s Allow = %q, want exactly GET", method, values)
				}
			})
		}
	}
}
