# todo-service

A small Go todo API whose writes flow through Kafka into Postgres, with Redis
as cache and idempotency store. Two processes: the API (`main.go`) and the
consumer (`cmd/consumer`).

## Flow

```text
 client
   |  POST /todos  {"title": ...}   [Idempotency-Key: k]
   v
+-------------------------- API (main.go) ---------------------------+
|  1. key given?  Redis SET NX idempotency:post-todos:k  (30s)       |
|       taken, done      -> 201 + original todo (Idempotent-Replayed)|
|       taken, in flight -> 409      other title -> 422              |
|       Redis error      -> 503                                      |
|  2. publish TodoCreated{id,title,created_at}, 10s cap ------+      |
|       fails -> release key, 502                             |      |
|  3. add to in-memory store, record todo under key (24h)     |      |
|  4. 201 {"id","title","done"}                               |      |
+-------------------------------------------------------------|------+
                                                              v
                                               Kafka topic todos.created
                                                              |
+------------------------ consumer (cmd/consumer) ------------|------+
|  5. fetch message  <----------------------------------------+      |
|  6. INSERT (event's created_at) ON CONFLICT (id) DO NOTHING        |
|       fails -> offset not committed, redelivered later             |
|  7. Redis DEL todos:durable:list   (failure only logged)           |
|  8. commit offset                                                  |
+--------------------------------------------------------------------+

 client
   |  GET /todos/durable
   v
 API: Redis GET todos:durable:list          (each Redis call capped at 100ms)
        hit  -> cached JSON
        miss -> Postgres SELECT ... ORDER BY created_at, id
                -> Redis SET todos:durable:list (30s) -> JSON
        Redis error -> read Postgres, no write-back
```

Startup, both processes:

```text
REDIS_ADDR set and PING ok? --no--> exit 1
            | yes
pgstore.New: connect to the already-migrated Postgres database
```

Apply schema changes separately before starting the service:

```sh
docker compose up -d
make db-migrate up
```

## Services

| Service | Role | Local port | Required by |
|---|---|---|---|
| Postgres 16 | Durable todos (`pgstore`) | 5433 | consumer; API for `/todos/durable` |
| Kafka 3.8 (KRaft) | `todos.created` events | 9094 | consumer; API publishes when set |
| Redis 7.4 | Idempotency keys, durable-list cache | 6380 | API and consumer |

## Configuration

| Variable | Used by | Default |
|---|---|---|
| `REDIS_ADDR` | API, consumer | required |
| `TODO_DATABASE_URL` | consumer (required), API (enables `/todos/durable`) | unset |
| `KAFKA_BROKERS` | consumer, API (unset: events not published) | consumer: `localhost:9094` |
| `KAFKA_TOPIC` | API, consumer | `todos.created` |
| `PORT` | API | `8080` |

## Endpoints

| Method and path | Behaviour |
|---|---|
| `GET /todos` | This API process's in-memory collection; optional exact `done=true` (completed) or `done=false` (incomplete) filter, other present values return 400 |
| `GET /todos/{id}` | Separate in-memory item lookup; `done` query parameters do not apply collection filtering or validation |
| `GET /todos/summary` | Synchronous counts of this API process's in-memory todos; not a durable or cluster-wide aggregate |
| `POST /todos` | Publish `todos.created`; optional `Idempotency-Key` header (max 255 chars) |
| `PUT`, `PATCH`, `DELETE /todos/{id}` | In-memory todos only |
| `GET /todos/durable` | Unchanged Postgres-backed collection, oldest `created_at` first (stamped by the API, not the consumer), cached in Redis for 30s; its behavior is separate from the in-memory `/todos` filter |

`GET /todos/summary` returns integer `total`, `done`, and `open` counts from
the in-memory store snapshot of the API process handling the request. It does
not read Postgres, Redis, Kafka, or another API process.

## Migrations

Migrations live in `migrations/` as paired
`NNNNNN_description.up.sql` and `.down.sql` files. Add the next version
without gaps; never edit a migration that has shipped. They are embedded in
the `db-migrate` command, so the command does not depend on the current
working directory.

| Command | Behaviour |
|---|---|
| `make db-migrate up` | Apply all pending migrations |
| `make db-migrate down` | Roll back all applied migrations |

Both commands use `TODO_DATABASE_URL`, defaulting to the Postgres address
published by `docker-compose.yml` (`localhost:5433`). `make verify-integration`
applies migrations before running integration tests, including when
Buildgate provides Postgres as a sidecar.

## Verify

| Command | Needs |
|---|---|
| `make test` | nothing (unit tests) |
| `make verify` | Docker; starts and removes the compose stack |
| `make verify-integration` | Postgres, Kafka and Redis already running (`BG_SERVICE_*` or `PG_ADDR`/`KAFKA_BROKERS`/`REDIS_ADDR`); applies migrations and is Buildgate's `verify_command` |

Every target runs `TestFunctionComplexity` (`complexity_test.go`): a function may have at most 25
decision points. `handleTodos` was over the limit when the check was added and is listed there as a
known exception; it may shrink, not grow.

## Design guide

`docs/design-guide/go-service.md` is the team's design guide: the decisions a spec must state and the
rules a plan must follow. `.factory.yml` names it (`design_guide: go-service`), so buildgate's spec
drafting and planning read it for every request against this repository. The operator's buildgate
session config must list the folder: `design_guide_dirs: [~/code/todo-kafka-service/docs/design-guide]`.
Service-specific facts the guide relies on are in `ARCHITECTURE.md`.
