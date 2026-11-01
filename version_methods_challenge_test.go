// buildgate-challenge: AC-3
package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChallengeVersionMethodsOnHTTPServers(t *testing.T) {
	protocols := []struct {
		name  string
		http2 bool
	}{
		{name: "HTTP1"},
		{name: "HTTP2", http2: true},
	}
	methods := []string{
		http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodTrace, http.MethodConnect,
		"PROPFIND", "M-SEARCH", "PURGE", "GETTING", "get", "Get",
	}
	for _, protocol := range protocols {
		t.Run(protocol.name, func(t *testing.T) {
			mux := http.NewServeMux()
			registerRoutes(mux)
			server := httptest.NewUnstartedServer(mux)
			if protocol.http2 {
				server.EnableHTTP2 = true
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			client := server.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
			for _, method := range methods {
				if protocol.http2 && method == http.MethodConnect {
					continue
				}
				t.Run(method, func(t *testing.T) {
					request, err := http.NewRequest(method, server.URL+"/version?probe=1", strings.NewReader("ignored"))
					if err != nil {
						t.Fatal(err)
					}
					request.Header.Set("Idempotency-Key", "must-not-matter")
					response, err := client.Do(request)
					if err != nil {
						t.Fatalf("%s /version: %v", method, err)
					}
					defer response.Body.Close()
					if protocol.http2 && response.ProtoMajor != 2 {
						t.Fatalf("response protocol = %s, want HTTP/2", response.Proto)
					}
					if response.StatusCode != http.StatusMethodNotAllowed {
						t.Fatalf("%s status = %d, want 405", method, response.StatusCode)
					}
					if allow := response.Header.Values("Allow"); len(allow) != 1 || allow[0] != "GET" {
						t.Fatalf("%s Allow = %q, want only GET", method, allow)
					}
					if method == http.MethodHead {
						body, err := io.ReadAll(response.Body)
						if err != nil || len(body) != 0 {
							t.Fatalf("HEAD response body = %q, error = %v", body, err)
						}
					}
				})
			}
		})
	}
}
