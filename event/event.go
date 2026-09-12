// Package event holds the wire format shared between the API (main.go,
// which publishes) and cmd/consumer (which reads) -- the two processes
// can't share package main directly, so this is their contract.
package event

// TodoCreated is published to TodosCreatedTopic on a successful
// POST /todos, and consumed by cmd/consumer to materialize the same todo
// into Postgres.
type TodoCreated struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// TodosCreatedTopic is the Kafka topic TodoCreated events are published to.
const TodosCreatedTopic = "todos.created"
