// buildgate-challenge: AC-3
package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVersionMethodsOnRootMuxChallenge(testContext *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	testContext.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	methods := []string{
		http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace,
		"PROPFIND", "MKCOL", "CUSTOM-METHOD", "get", "Get",
	}
	for _, method := range methods {
		testContext.Run(method, func(testContext *testing.T) {
			request, err := http.NewRequest(method, server.URL+"/version?service=other", strings.NewReader("ignored"))
			if err != nil {
				testContext.Fatal(err)
			}
			request.Header.Set("X-HTTP-Method-Override", "GET")
			request.Header.Set("Origin", "https://example.test")
			request.Header.Set("Access-Control-Request-Method", "GET")
			response, err := client.Do(request)
			if err != nil {
				testContext.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusMethodNotAllowed {
				testContext.Errorf("%s /version: status = %d, want 405", method, response.StatusCode)
			}
			if got := response.Header.Values("Allow"); !reflect.DeepEqual(got, []string{"GET"}) {
				testContext.Errorf("%s /version: Allow = %q, want GET", method, got)
			}
		})
	}
}
