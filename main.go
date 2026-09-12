package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"todo-service/event"
	"todo-service/pgstore"
)

type Todo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type Store struct {
	mu    sync.RWMutex
	todos []Todo
}

func NewStore() *Store {
	return &Store{todos: []Todo{}}
}

func (s *Store) List() []Todo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Todo, len(s.todos))
	copy(out, s.todos)
	return out
}

// AddWithID inserts a todo under an id the caller already generated (see
// newTodoID), rather than assigning one here, so the same id can be used
// both for this immediate in-memory response and for the Kafka event a
// worker later writes into Postgres under.
func (s *Store) AddWithID(id, title string) Todo {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := Todo{ID: id, Title: title}
	s.todos = append(s.todos, t)
	return t
}

func (s *Store) Get(id string) (Todo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.todos {
		if t.ID == id {
			return t, true
		}
	}
	return Todo{}, false
}

func (s *Store) Update(id string, title string, done bool) (Todo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.todos {
		if t.ID == id {
			t.Title = title
			t.Done = done
			s.todos[i] = t
			return t, true
		}
	}
	return Todo{}, false
}

func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.todos {
		if t.ID == id {
			s.todos = append(s.todos[:i], s.todos[i+1:]...)
			return true
		}
	}
	return false
}

var store = NewStore()

// DurableLister is the seam between handleTodosDurable and the real
// pgstore.Store, so main_test.go's unit tests never need a live Postgres
// -- the same injectable-dependency shape publisher uses in
// publisher.go. pgstore.Store already satisfies this shape.
type DurableLister interface {
	List() ([]pgstore.Todo, error)
}

// durableStore is nil by default so `go test ./...` and any run without
// TODO_DATABASE_URL set don't require a reachable Postgres -- main wires
// in a real pgstore.Store only when TODO_DATABASE_URL is set.
var durableStore DurableLister

func handleTodos(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/todos")
	if path != "" {
		path = strings.TrimPrefix(path, "/")
	}

	switch r.Method {
	case http.MethodGet:
		if path == "" || path == "/" {
			jsonNewline(store.List(), w)
			return
		}
		if t, ok := store.Get(path); ok {
			jsonNewline(t, w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return

	case http.MethodPost:
		var body struct{ Title string }
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Title == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Publish first, cache second: the in-memory Store below is only
		// this process's own read cache for an immediate response --
		// Postgres, materialized by cmd/consumer off this same event, is
		// the durable store. A publish failure must not report success
		// with nothing behind it, so the store is only updated once the
		// event is confirmed queued.
		id := newTodoID()
		if err := publisher.Publish(r.Context(), event.TodoCreated{ID: id, Title: body.Title}); err != nil {
			log.Printf("publish todo.created for %s: %v", id, err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		t := store.AddWithID(id, body.Title)
		w.WriteHeader(http.StatusCreated)
		jsonNewline(t, w)
		return

	case http.MethodPut:
		if path == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct {
			Title string `json:"title"`
			Done  bool   `json:"done"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if t, ok := store.Update(path, body.Title, body.Done); ok {
			jsonNewline(t, w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return

	case http.MethodDelete:
		if path == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if store.Delete(path) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func handleTodosDurable(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if durableStore == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		todos, err := durableStore.List()
		if err != nil {
			log.Printf("list durable todos: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		jsonNewline(todos, w)
		return
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func jsonNewline(v any, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func main() {
	// publisher stays the noopPublisher default (see publisher.go) unless
	// KAFKA_BROKERS is set -- so running this binary with no Kafka
	// configured behaves exactly as it did before Kafka was added.
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		topic := os.Getenv("KAFKA_TOPIC")
		if topic == "" {
			topic = event.TodosCreatedTopic
		}
		kp := NewKafkaPublisher(strings.Split(brokers, ","), topic)
		defer kp.Close()
		publisher = kp
		log.Printf("publishing %s to kafka brokers %s, topic %q", event.TodosCreatedTopic, brokers, topic)
	}

	// durableStore stays nil (see DurableLister above) unless
	// TODO_DATABASE_URL is set -- a missing or unreachable Postgres must
	// not prevent the API from serving its existing endpoints, so a
	// connect failure only logs and continues, mirroring how the Kafka
	// wiring above simply does nothing when KAFKA_BROKERS is unset.
	if dsn := os.Getenv("TODO_DATABASE_URL"); dsn != "" {
		ds, err := pgstore.New(dsn)
		if err != nil {
			log.Printf("connect durable store: %v -- /todos/durable will be unavailable", err)
		} else {
			defer ds.Close()
			durableStore = ds
			log.Printf("durable store connected")
		}
	}

	http.HandleFunc("/todos", handleTodos)
	http.HandleFunc("/todos/", handleTodos)
	http.HandleFunc("/todos/durable", handleTodosDurable)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Printf("listening on :%s\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("listen on :%s: %v", port, err)
	}
}
