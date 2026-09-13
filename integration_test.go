//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"todo-service/event"
	"todo-service/pgstore"
)

// This file backs `make verify-integration` (see the Makefile): real
// Postgres/Kafka round trips through the same DurableLister (main.go)
// and EventPublisher (publisher.go) seams the rest of this repo already
// uses to keep `go test ./...` Docker-free, wired here to their real
// implementations -- pgstore.Store and KafkaPublisher -- instead of the
// fakes/noop those unit tests use. Excluded from the default `go test
// ./...` run (no "integration" build tag).
//
// TestMain dials both addresses with a short timeout before running any
// test, so a caller that forgot to launch Postgres/Kafka (or a
// sandboxed run whose ComposeServicesLifecycle sidecars failed to come
// up) gets one clear line naming which service is unreachable, not a
// hang or a raw driver stack trace.

const dialTimeout = 5 * time.Second

func TestMain(m *testing.M) {
	pgAddr := os.Getenv("PG_ADDR")
	kafkaAddr := firstBroker(os.Getenv("KAFKA_BROKERS"))
	if pgAddr == "" || kafkaAddr == "" {
		fmt.Fprintln(os.Stderr, "verify-integration: PG_ADDR and KAFKA_BROKERS must both be set -- run via `make verify-integration`, not `go test` directly")
		os.Exit(1)
	}
	for _, svc := range []struct{ name, addr string }{
		{"postgres", pgAddr},
		{"kafka", kafkaAddr},
	} {
		conn, err := net.DialTimeout("tcp", svc.addr, dialTimeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "verify-integration: %s unreachable at %s: %v -- was it launched (docker compose, or a sandboxed run's ComposeServicesLifecycle) before this target ran?\n", svc.name, svc.addr, err)
			os.Exit(1)
		}
		conn.Close()
	}
	os.Exit(m.Run())
}

func firstBroker(brokers string) string {
	if brokers == "" {
		return ""
	}
	return strings.SplitN(brokers, ",", 2)[0]
}

// TestDurableStoreRoundTrip writes one row through the real pgstore.Store
// -- the same type main.go wires in as DurableLister when
// TODO_DATABASE_URL is set -- and reads it back through the DurableLister
// interface, exercising the real seam rather than a fake.
func TestDurableStoreRoundTrip(t *testing.T) {
	dsn := requireEnv(t, "TODO_DATABASE_URL")

	ds, err := pgstore.New(dsn)
	if err != nil {
		t.Fatalf("connect to postgres at %s: %v", dsn, err)
	}
	defer ds.Close()

	var lister DurableLister = ds

	id := fmt.Sprintf("verify-integration-%d", time.Now().UnixNano())
	const title = "integration round trip"
	if err := ds.AddWithID(id, title); err != nil {
		t.Fatalf("AddWithID: %v", err)
	}
	t.Cleanup(func() { ds.Delete(id) })

	todos, err := lister.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, td := range todos {
		if td.ID == id && td.Title == title {
			return
		}
	}
	t.Fatalf("round-tripped todo %s not found in List(): %+v", id, todos)
}

// TestKafkaPublisherRoundTrip publishes one event through the real
// KafkaPublisher -- the same type main.go wires in as EventPublisher when
// KAFKA_BROKERS is set -- and reads it back with a plain Kafka reader,
// exercising the real seam rather than the noopPublisher unit tests use.
func TestKafkaPublisherRoundTrip(t *testing.T) {
	brokersEnv := requireEnv(t, "KAFKA_BROKERS")
	brokers := strings.Split(brokersEnv, ",")
	// A topic distinct from event.TodosCreatedTopic so this test never
	// collides with a real API/consumer pair also running against the
	// same broker.
	topic := "todo-service.verify-integration"

	// KafkaPublisher (kafka.go) doesn't set AllowAutoTopicCreation, so a
	// first Publish against a brand new topic fails with "Unknown Topic
	// Or Partition" rather than creating it -- create it explicitly here
	// so this test exercises the real Publish/Fetch round trip, not
	// topic-creation races. CreateTopics is idempotent, so this is safe
	// to call even if a previous run already created it.
	if err := createTopic(brokers[0], topic, dialTimeout); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}

	pub := NewKafkaPublisher(brokers, topic)
	defer pub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	want := event.TodoCreated{
		ID:    fmt.Sprintf("verify-integration-%d", time.Now().UnixNano()),
		Title: "kafka round trip",
	}
	if err := pub.Publish(ctx, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: "todo-service-verify-integration",
		// See cmd/consumer's own comment on this field: without it, a
		// reader that joins the group before the topic exists can
		// stabilize with zero partitions and never notice the topic
		// this test's own Publish call just auto-created.
		WatchPartitionChanges: true,
	})
	defer reader.Close()

	msg, err := reader.FetchMessage(ctx)
	if err != nil {
		t.Fatalf("FetchMessage: %v", err)
	}

	var got event.TodoCreated
	if err := json.Unmarshal(msg.Value, &got); err != nil {
		t.Fatalf("unmarshal round-tripped message: %v", err)
	}
	if got != want {
		t.Fatalf("round-tripped event mismatch: want %+v got %+v", want, got)
	}
}

func createTopic(broker, topic string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := kafka.DialContext(ctx, "tcp", broker)
	if err != nil {
		return fmt.Errorf("dial %s: %w", broker, err)
	}
	defer conn.Close()
	return conn.CreateTopics(kafka.TopicConfig{
		Topic:             topic,
		NumPartitions:     1,
		ReplicationFactor: 1,
	})
}

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s not set; run via `make verify-integration`", key)
	}
	return v
}
