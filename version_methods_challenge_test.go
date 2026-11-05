// buildgate-challenge: AC-3 subject: main.go
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChallengeVersionRouteRejectsNonGETMethods(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	methods := []string{
		http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodTrace, http.MethodConnect,
		"get", "head", "PROPFIND", "CUSTOM", "GETX",
	}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			request, err := http.NewRequest(method, server.URL+"/version", nil)
			if err != nil {
				t.Fatal(err)
			}
			if method == http.MethodOptions {
				request.Header.Set("Origin", "https://example.com")
				request.Header.Set("Access-Control-Request-Method", http.MethodGet)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s /version status = %d, want 405", method, response.StatusCode)
			}
			values := response.Header.Values("Allow")
			if len(values) == 0 {
				t.Fatal("missing Allow: GET header")
			}
			for _, value := range values {
				for _, allowed := range strings.Split(value, ",") {
					if strings.TrimSpace(allowed) != http.MethodGet {
						t.Errorf("Allow = %q, want only GET", values)
					}
				}
			}
		})
	}
}
