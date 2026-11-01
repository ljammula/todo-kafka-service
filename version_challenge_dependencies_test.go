// buildgate-challenge: AC-4
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"todo-service/event"
	"todo-service/pgstore"
)

type challengeUnavailableDependencies struct {
	postgres atomic.Int32
	kafka    atomic.Int32
	redis    atomic.Int32
}

func (deps *challengeUnavailableDependencies) List() ([]pgstore.Todo, error) {
	deps.postgres.Add(1)
	return nil, errors.New("postgres unavailable")
}

func (deps *challengeUnavailableDependencies) Publish(context.Context, event.TodoCreated) error {
	deps.kafka.Add(1)
	return errors.New("kafka unavailable")
}

func (deps *challengeUnavailableDependencies) Claim(context.Context, string, string) (*idempotencyRecord, error) {
	deps.redis.Add(1)
	return nil, errors.New("redis unavailable")
}

func (deps *challengeUnavailableDependencies) Complete(context.Context, string, idempotencyRecord) error {
	deps.redis.Add(1)
	return errors.New("redis unavailable")
}

func (deps *challengeUnavailableDependencies) Release(context.Context, string) error {
	deps.redis.Add(1)
	return errors.New("redis unavailable")
}

func TestChallengeVersionDoesNotTouchUnavailableDependencies(t *testing.T) {
	postgres := challengeConnectionTrap(t)
	kafka := challengeConnectionTrap(t)
	redis := challengeConnectionTrap(t)
	t.Setenv("TODO_DATABASE_URL", "postgres://unused:unused@"+postgres.Addr().String()+"/unused?sslmode=disable&connect_timeout=1")
	t.Setenv("KAFKA_BROKERS", kafka.Addr().String())
	t.Setenv("REDIS_ADDR", redis.Addr().String())

	deps := &challengeUnavailableDependencies{}
	oldStore, oldPublisher, oldIdempotency := durableStore, publisher, idempotency
	durableStore, publisher, idempotency = deps, deps, deps
	t.Cleanup(func() { durableStore, publisher, idempotency = oldStore, oldPublisher, oldIdempotency })

	mux := http.NewServeMux()
	registerRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second}

	var workers sync.WaitGroup
	for index := 0; index < 24; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			challengeRequestVersionWithoutDependencies(t, client, server.URL)
		}()
	}
	workers.Wait()

	challengeAssertNoConnections(t, "postgres", postgres)
	challengeAssertNoConnections(t, "kafka", kafka)
	challengeAssertNoConnections(t, "redis", redis)
	if got := deps.postgres.Load(); got != 0 {
		t.Errorf("postgres calls = %d, want 0", got)
	}
	if got := deps.kafka.Load(); got != 0 {
		t.Errorf("kafka calls = %d, want 0", got)
	}
	if got := deps.redis.Load(); got != 0 {
		t.Errorf("redis calls = %d, want 0", got)
	}
}

func challengeRequestVersionWithoutDependencies(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/version", nil)
	if err != nil {
		t.Error(err)
		return
	}
	req.Header.Set("Idempotency-Key", "must-not-touch-redis")
	resp, err := client.Do(req)
	if err != nil {
		t.Error(err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("status = %d, Content-Type = %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Error(err)
		return
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil || len(result) != 1 || result["service"] != "todo-kafka-service" {
		t.Errorf("response = %q, error = %v", body, err)
	}
}

func challengeConnectionTrap(t *testing.T) *net.TCPListener {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

func challengeAssertNoConnections(t *testing.T, service string, listener *net.TCPListener) {
	t.Helper()
	if err := listener.SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if err == nil {
		conn.Close()
		t.Errorf("version request connected to %s", service)
		return
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("checking %s connections: %v", service, err)
	}
}
