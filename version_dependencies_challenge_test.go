// buildgate-challenge: AC-4
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"todo-service/event"
	"todo-service/pgstore"
)

type challengeVersionDependencies struct {
	durableCalls     atomic.Int32
	publishCalls     atomic.Int32
	idempotencyCalls atomic.Int32
}

func (dependencies *challengeVersionDependencies) List() ([]pgstore.Todo, error) {
	dependencies.durableCalls.Add(1)
	return nil, errors.New("unexpected cache or Postgres read")
}

func (dependencies *challengeVersionDependencies) Publish(context.Context, event.TodoCreated) error {
	dependencies.publishCalls.Add(1)
	return errors.New("unexpected Kafka publish")
}

func (dependencies *challengeVersionDependencies) Claim(context.Context, string, string) (*idempotencyRecord, error) {
	dependencies.idempotencyCalls.Add(1)
	return nil, errors.New("unexpected Redis claim")
}

func (dependencies *challengeVersionDependencies) Complete(context.Context, string, idempotencyRecord) error {
	dependencies.idempotencyCalls.Add(1)
	return errors.New("unexpected Redis completion")
}

func (dependencies *challengeVersionDependencies) Release(context.Context, string) error {
	dependencies.idempotencyCalls.Add(1)
	return errors.New("unexpected Redis release")
}

func challengeUnavailableService(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	calls := &atomic.Int32{}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			calls.Add(1)
			connection.Close()
		}
	}()
	return listener.Addr().String(), calls
}

func TestChallengeVersionWithoutDependencies(t *testing.T) {
	previousDurable, previousPublisher, previousIdempotency := durableStore, publisher, idempotency
	t.Cleanup(func() {
		durableStore, publisher, idempotency = previousDurable, previousPublisher, previousIdempotency
	})
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", configured), func(t *testing.T) {
			dependencies := &challengeVersionDependencies{}
			var postgresConnections, kafkaConnections, redisConnections *atomic.Int32
			if configured {
				postgresAddress, postgresCount := challengeUnavailableService(t)
				kafkaAddress, kafkaCount := challengeUnavailableService(t)
				redisAddress, redisCount := challengeUnavailableService(t)
				postgresConnections, kafkaConnections, redisConnections = postgresCount, kafkaCount, redisCount
				t.Setenv("TODO_DATABASE_URL", "postgres://todo:todo@"+postgresAddress+"/todo?sslmode=disable")
				t.Setenv("KAFKA_BROKERS", kafkaAddress)
				t.Setenv("REDIS_ADDR", redisAddress)
				durableStore, publisher, idempotency = dependencies, dependencies, dependencies
			} else {
				t.Setenv("TODO_DATABASE_URL", "")
				t.Setenv("KAFKA_BROKERS", "")
				t.Setenv("REDIS_ADDR", "")
				durableStore, publisher, idempotency = nil, nil, nil
			}

			mux := http.NewServeMux()
			registerRoutes(mux)
			server := httptest.NewServer(mux)
			defer server.Close()
			client := server.Client()
			client.Timeout = 3 * time.Second
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
			for requestNumber := 0; requestNumber < 4; requestNumber++ {
				request, err := http.NewRequest(http.MethodGet, server.URL+"/version?probe=dependency", strings.NewReader("ignored"))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Idempotency-Key", "must-not-be-claimed")
				response, err := client.Do(request)
				if err != nil {
					t.Fatalf("GET /version: %v", err)
				}
				var body map[string]any
				decodeErr := json.NewDecoder(response.Body).Decode(&body)
				response.Body.Close()
				if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" || decodeErr != nil || len(body) != 1 || body["service"] != "todo-kafka-service" {
					t.Fatalf("GET /version = status %d, Content-Type %q, body %#v, decode error %v", response.StatusCode, response.Header.Get("Content-Type"), body, decodeErr)
				}
			}
			if configured {
				time.Sleep(100 * time.Millisecond)
				if dependencies.durableCalls.Load() != 0 || dependencies.publishCalls.Load() != 0 || dependencies.idempotencyCalls.Load() != 0 {
					t.Errorf("unexpected dependency calls: durable=%d, publisher=%d, idempotency=%d", dependencies.durableCalls.Load(), dependencies.publishCalls.Load(), dependencies.idempotencyCalls.Load())
				}
				if postgresConnections.Load() != 0 || kafkaConnections.Load() != 0 || redisConnections.Load() != 0 {
					t.Errorf("unexpected network connections: Postgres=%d, Kafka=%d, Redis=%d", postgresConnections.Load(), kafkaConnections.Load(), redisConnections.Load())
				}
			}
		})
	}
}
