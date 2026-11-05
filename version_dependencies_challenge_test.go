// buildgate-challenge: AC-4 subject: main.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"todo-service/cache"
	"todo-service/event"
	"todo-service/pgstore"
)

type challengeUnavailableDependencies struct {
	calls []string
}

func (dependencies *challengeUnavailableDependencies) List() ([]pgstore.Todo, error) {
	dependencies.calls = append(dependencies.calls, "durable store or cache List")
	return nil, errors.New("unavailable")
}

func (dependencies *challengeUnavailableDependencies) Publish(context.Context, event.TodoCreated) error {
	dependencies.calls = append(dependencies.calls, "Kafka Publish")
	return errors.New("unavailable")
}

func (dependencies *challengeUnavailableDependencies) Claim(context.Context, string, string) (*idempotencyRecord, error) {
	dependencies.calls = append(dependencies.calls, "Redis Claim")
	return nil, errors.New("unavailable")
}

func (dependencies *challengeUnavailableDependencies) Complete(context.Context, string, idempotencyRecord) error {
	dependencies.calls = append(dependencies.calls, "Redis Complete")
	return errors.New("unavailable")
}

func (dependencies *challengeUnavailableDependencies) Release(context.Context, string) error {
	dependencies.calls = append(dependencies.calls, "Redis Release")
	return errors.New("unavailable")
}

func (dependencies *challengeUnavailableDependencies) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		dependencies.calls = append(dependencies.calls, "Redis Dial")
		return nil, errors.New("unavailable")
	}
}

func (dependencies *challengeUnavailableDependencies) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, command redis.Cmder) error {
		dependencies.calls = append(dependencies.calls, "Redis "+command.Name())
		return errors.New("unavailable")
	}
}

func (dependencies *challengeUnavailableDependencies) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(context.Context, []redis.Cmder) error {
		dependencies.calls = append(dependencies.calls, "Redis pipeline")
		return errors.New("unavailable")
	}
}

func TestChallengeVersionDoesNotUseUnavailableDependencies(t *testing.T) {
	tests := []struct {
		name       string
		configured bool
		redisCache bool
	}{
		{name: "unconfigured"},
		{name: "unavailable direct dependencies", configured: true},
		{name: "unavailable Redis cache and dependencies", configured: true, redisCache: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			previousDurable, previousPublisher, previousIdempotency := durableStore, publisher, idempotency
			t.Cleanup(func() {
				durableStore, publisher, idempotency = previousDurable, previousPublisher, previousIdempotency
			})
			dependencies := &challengeUnavailableDependencies{}
			if test.configured {
				t.Setenv("TODO_DATABASE_URL", "postgres://localhost:1/unavailable")
				t.Setenv("KAFKA_BROKERS", "localhost:1")
				t.Setenv("REDIS_ADDR", "localhost:1")
				durableStore, publisher, idempotency = dependencies, dependencies, dependencies
			} else {
				t.Setenv("TODO_DATABASE_URL", "")
				t.Setenv("KAFKA_BROKERS", "")
				t.Setenv("REDIS_ADDR", "")
				durableStore, publisher, idempotency = nil, nil, nil
			}
			if test.redisCache {
				client := redis.NewClient(&redis.Options{Addr: "localhost:1"})
				client.AddHook(dependencies)
				t.Cleanup(func() { client.Close() })
				durableStore = cache.NewListCache(client, dependencies, time.Minute)
				idempotency = redisIdempotencyStore{rdb: client}
			}

			mux := http.NewServeMux()
			registerRoutes(mux)
			request := httptest.NewRequest(http.MethodGet, "/version", nil)
			request.Header.Set("Idempotency-Key", "version-request")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", response.Code)
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Errorf("decode version response: %v", err)
			} else if want := map[string]any{"service": "todo-kafka-service"}; !reflect.DeepEqual(body, want) {
				t.Errorf("body = %#v, want %#v", body, want)
			}
			if len(dependencies.calls) != 0 {
				t.Errorf("version request called dependencies: %v", dependencies.calls)
			}
		})
	}
}
