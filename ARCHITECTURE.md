# Todo Service — Architecture

## Repo layout

| Path | What it is |
|---|---|
| `main.go`, `id.go`, `idempotency.go`, `kafka.go`, `publisher.go`, `summary.go` | The API process (package `main`): HTTP handlers, the in-memory todo store, the idempotency store, the Kafka publisher |
| `cmd/consumer/` | The consumer process: `todos.created` events into Postgres |
| `pgstore/` | Postgres access |
| `migrations/`, `internal/dbmigrate/`, `cmd/db-migrate/` | Embedded SQL migrations and the `golang-migrate` command |
| `cache/` | Redis read-through cache for the durable list |
| `event/` | The `TodoCreated` event type |
| `docs/design-guide/go-service.md` | The team design guide buildgate's spec drafting and planning read (`design_guide: go-service` in `.factory.yml`) |

Handlers live in package `main` and reach their dependencies through
interfaces declared there (`DurableLister`, `EventPublisher`,
`IdempotencyStore`). New dependencies follow the same shape; there is no
separate service package.

## Service facts for design decisions

| Fact | Value |
|---|---|
| Processes | Two: the API and the consumer. The API keeps its own in-memory todos, so they are per process |
| Write path | `POST /todos` publishes `todos.created` to Kafka; the consumer inserts into Postgres. A todo is durable only after the consumer has handled its event |
| Delivery contract | At-least-once: the consumer inserts with `ON CONFLICT (id) DO NOTHING` and commits the offset after the insert |
| Cache | Redis, shared: `todos:durable:list`, TTL 30 s, deleted by the consumer after an insert. A Redis error reads Postgres and skips the write-back |
| Idempotency | `Idempotency-Key` on `POST /todos`: Redis claim for 30 s, result kept 24 h |
| Timeouts in use | Redis cache call 100 ms; idempotency store call 2 s; Kafka publish 10 s |
| Other services called | None |

## Verification

- `make verify-integration`: buildgate's `verify_command`. Runs against the Postgres, Kafka and Redis sidecars buildgate starts from `docker-compose.yml` (`BG_SERVICE_*`); the sandboxed worker has no Docker, so `make verify`, which starts the stack itself, can't run there.
- Every target runs `TestFunctionComplexity`: at most 25 decision points per function.

## Known deviations

- `handleTodos` has 36 decision points, over the team limit; listed in `complexity_test.go` as an exception that may shrink but not grow.
- Handlers make their store and cache calls directly; the guide's service type applies only to new logic that combines more than one call.
