// buildgate-challenge: AC-4
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"todo-service/event"
	"todo-service/pgstore"
)

type challengeUnavailableDependencies struct {
	postgres atomic.Int64
	kafka    atomic.Int64
	redis    atomic.Int64
}

func (dependencies *challengeUnavailableDependencies) List() ([]pgstore.Todo, error) {
	dependencies.postgres.Add(1)
	return nil, errors.New("postgres and list cache unavailable")
}

func (dependencies *challengeUnavailableDependencies) Publish(context.Context, event.TodoCreated) error {
	dependencies.kafka.Add(1)
	return errors.New("kafka unavailable")
}

func (dependencies *challengeUnavailableDependencies) Claim(context.Context, string, string) (*idempotencyRecord, error) {
	dependencies.redis.Add(1)
	return nil, errors.New("redis unavailable")
}

func (dependencies *challengeUnavailableDependencies) Complete(context.Context, string, idempotencyRecord) error {
	dependencies.redis.Add(1)
	return errors.New("redis unavailable")
}

func (dependencies *challengeUnavailableDependencies) Release(context.Context, string) error {
	dependencies.redis.Add(1)
	return errors.New("redis unavailable")
}

func TestChallengeVersionDoesNotUseUnavailableDependencies(t *testing.T) {
	previousStore, previousPublisher, previousIdempotency := durableStore, publisher, idempotency
	defer func() {
		durableStore, publisher, idempotency = previousStore, previousPublisher, previousIdempotency
	}()

	for _, configured := range []bool{false, true} {
		name := "unconfigured"
		if configured {
			name = "configured but unavailable"
		}
		t.Run(name, func(t *testing.T) {
			dependencies := &challengeUnavailableDependencies{}
			if configured {
				durableStore, publisher, idempotency = dependencies, dependencies, dependencies
			} else {
				durableStore, publisher, idempotency = nil, nil, nil
			}
			mux := http.NewServeMux()
			registerRoutes(mux)
			server := httptest.NewServer(mux)
			defer server.Close()
			client := &http.Client{Timeout: 3 * time.Second}
			for _, target := range []string{"/version", "/version?done=true&limit=1", "/version?service=other"} {
				request, err := http.NewRequest(http.MethodGet, server.URL+target, nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Idempotency-Key", "dependency-probe")
				response, err := client.Do(request)
				if err != nil {
					t.Fatalf("GET %s: %v", target, err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("GET %s: status = %d, Content-Type = %q", target, response.StatusCode, response.Header.Get("Content-Type"))
				}
				var object map[string]string
				if err := json.Unmarshal(body, &object); err != nil || len(object) != 1 || object["service"] != "todo-kafka-service" {
					t.Fatalf("GET %s: body = %q, decode error = %v", target, body, err)
				}
			}
			if configured {
				time.Sleep(100 * time.Millisecond)
				if pg, kafka, redis := dependencies.postgres.Load(), dependencies.kafka.Load(), dependencies.redis.Load(); pg != 0 || kafka != 0 || redis != 0 {
					t.Errorf("dependency calls: postgres/cache = %d, kafka = %d, redis = %d; want none", pg, kafka, redis)
				}
			}
		})
	}
}
