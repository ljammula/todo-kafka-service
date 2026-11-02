// buildgate-challenge: AC-4
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"todo-service/cache"
	"todo-service/event"
	"todo-service/pgstore"
)

type unavailableVersionListerChallenge struct{ calls atomic.Int32 }

func (lister *unavailableVersionListerChallenge) List() ([]pgstore.Todo, error) {
	lister.calls.Add(1)
	return nil, errors.New("postgres unavailable")
}

type unavailableVersionPublisherChallenge struct{ calls atomic.Int32 }

func (publisher *unavailableVersionPublisherChallenge) Publish(context.Context, event.TodoCreated) error {
	publisher.calls.Add(1)
	return errors.New("kafka unavailable")
}

func TestVersionAvoidsUnavailableDependenciesChallenge(testContext *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		testContext.Fatal(err)
	}
	var connections atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			conn.Close()
		}
	}()
	testContext.Cleanup(func() { listener.Close(); <-done })
	address := listener.Addr().String()
	testContext.Setenv("REDIS_ADDR", address)
	testContext.Setenv("KAFKA_BROKERS", address)
	testContext.Setenv("TODO_DATABASE_URL", fmt.Sprintf("postgres://test:test@%s/test?sslmode=disable&connect_timeout=1", address))

	var redisDials atomic.Int32
	redisClient := redis.NewClient(&redis.Options{
		Addr: address,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			redisDials.Add(1)
			return nil, errors.New("redis unavailable")
		},
		MaxRetries: -1,
	})
	testContext.Cleanup(func() { redisClient.Close() })
	postgres := &unavailableVersionListerChallenge{}
	kafka := &unavailableVersionPublisherChallenge{}
	previousStore, previousPublisher, previousIdempotency := durableStore, publisher, idempotency
	durableStore = cache.NewListCache(redisClient, postgres, time.Second)
	publisher = kafka
	idempotency = redisIdempotencyStore{rdb: redisClient}
	testContext.Cleanup(func() { durableStore, publisher, idempotency = previousStore, previousPublisher, previousIdempotency })

	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	testContext.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	for requestNumber := 0; requestNumber < 5; requestNumber++ {
		response, err := client.Get(server.URL + "/version")
		if err != nil {
			testContext.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			testContext.Fatal(err)
		}
		var document map[string]any
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" || json.Unmarshal(body, &document) != nil || len(document) != 1 || document["service"] != "todo-kafka-service" {
			testContext.Fatalf("GET /version = %d, %q, %s", response.StatusCode, response.Header.Get("Content-Type"), body)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if got := postgres.calls.Load(); got != 0 {
		testContext.Errorf("version called postgres %d times", got)
	}
	if got := kafka.calls.Load(); got != 0 {
		testContext.Errorf("version published to kafka %d times", got)
	}
	if got := redisDials.Load(); got != 0 {
		testContext.Errorf("version dialed redis %d times", got)
	}
	if got := connections.Load(); got != 0 {
		testContext.Errorf("version made %d configured upstream connections", got)
	}
}

func TestVersionWithoutConfiguredDependenciesChallenge(testContext *testing.T) {
	testContext.Setenv("REDIS_ADDR", "")
	testContext.Setenv("KAFKA_BROKERS", "")
	testContext.Setenv("TODO_DATABASE_URL", "")
	previousStore, previousPublisher, previousIdempotency := durableStore, publisher, idempotency
	durableStore, publisher, idempotency = nil, noopPublisher{}, nil
	testContext.Cleanup(func() { durableStore, publisher, idempotency = previousStore, previousPublisher, previousIdempotency })

	mux := http.NewServeMux()
	registerRoutes(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/version", nil))
	var document map[string]any
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" || json.Unmarshal(recorder.Body.Bytes(), &document) != nil || len(document) != 1 || document["service"] != "todo-kafka-service" {
		testContext.Fatalf("GET /version = %d, %q, %s", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
}
