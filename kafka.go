package main

import (
	"context"
	"encoding/json"

	"github.com/segmentio/kafka-go"

	"todo-service/event"
)

// KafkaPublisher is the real EventPublisher (see publisher.go), backed by
// a Kafka producer against the broker docker-compose.yml's own "kafka"
// service runs.
type KafkaPublisher struct {
	writer *kafka.Writer
}

func NewKafkaPublisher(brokers []string, topic string) *KafkaPublisher {
	return &KafkaPublisher{writer: &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.LeastBytes{},
		RequiredAcks: kafka.RequireOne,
	}}
}

func (p *KafkaPublisher) Publish(ctx context.Context, evt event.TodoCreated) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	// Keyed by id so any future partitioning still routes every event for
	// one todo through the same partition, preserving per-todo ordering.
	return p.writer.WriteMessages(ctx, kafka.Message{Key: []byte(evt.ID), Value: body})
}

func (p *KafkaPublisher) Close() error { return p.writer.Close() }
