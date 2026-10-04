# Go service design guide

The team's rules for what to decide before code is written, shared by every
Go service that names this guide (`design_guide: go-service` in its
`.factory.yml`). Buildgate gives the text under "Spec decisions" to spec
drafting and the text under "Plan rules" to planning; this introduction is
for people and is not sent to either.

Numbers marked "team default" apply unless the service's own
`ARCHITECTURE.md` states a different one. Facts about one service (its
upstreams, replica count, latency targets, existing caches and topics)
belong in that service's `ARCHITECTURE.md`, not here.

## Spec decisions

Answer each question below for every request and state the answer, with its
reason, in the part of the spec it changes. "Not applicable: <reason>" is a
valid answer. A bug fix that changes no interface answers "Not applicable"
to all five and adds nothing.

1. Delivery. Does the caller need the result in the response?
   - Yes, and the work is one bounded read or write that finishes within
     500 ms (team default): synchronous.
   - No, and the work must survive a restart or be retried until it
     succeeds (a notification, a write to another system, anything a user
     would notice missing): publish an event to Kafka and do the work in a
     consumer. The endpoint does not wait for it. State the delivery
     contract: at-least-once with an idempotent consumer, and what the
     caller sees if the publish itself fails.
   - No, and losing the work on a restart is acceptable (a metric, a cache
     warm-up): a goroutine from a bounded pool. State what is lost on a
     restart.
   - The request does not say whether the caller needs the result, or the
     work may run longer than the synchronous budget (a bulk operation):
     raise it as a decision for the operator, with the synchronous and the
     Kafka option both described.
2. Caching. Is the same data read far more often than it changes?
   - No, or the request does not say: no cache.
   - Yes, and each replica may serve its own copy for the stated time:
     in-memory, with a size bound and a TTL.
   - Yes, and all replicas must agree within a stated time, or the service
     runs more than one replica and the data is user-visible: the shared
     Redis cache, read-through. State the TTL, the staleness bound it
     gives, and what invalidates the entry. A Redis failure must fall back
     to the source, never fail the request.
3. New infrastructure. Does the change add an endpoint, a consumer, a
   topic, a table or column, a cache key, a configuration variable or an
   external dependency? Name each one. Add only what questions 1 and 2
   call for.
4. Upstream calls. For each call to another service, a database, a broker
   or a cache: its timeout, what the caller sees when it fails or times
   out, and whether a partial result is ever returned. Timeouts (team
   defaults): 100 ms for a cache call, 500 ms for a database read or an
   HTTP call to another service made while serving a request, 2 s for a
   database write, 10 s for a Kafka publish, 5 s for a call a consumer
   makes outside any request. The default is no partial result: the
   request fails with 502 for an upstream error, 504 for an upstream
   timeout and 503 for a dependency that is not configured.
5. Stored data. Does the change alter the shape of stored data or of an
   event? State how old rows or old events are handled, and that the
   change is safe while the previous version of the service is still
   running (add before use; never rename or drop in the same change).

## Plan rules

- Tickets. One ticket per endpoint, consumer or migration unless its change
  would pass about 400 changed lines. Never split one endpoint across
  tickets by layer.
- Structure. A handler parses the request, calls one function and writes
  the response; it holds no business logic. Put logic that combines more
  than one store or client call, or that has rules of its own, in a
  service type with no HTTP or SQL import. A handler that makes a single
  store or client call needs no service type. Follow the package layout
  the repository already has; do not move existing code to match these
  rules.
- Dependencies. Code reaches a database, broker, cache or other service
  through an interface declared in the package that uses it, with only the
  methods it needs. Unit tests use a fake of that interface, never a live
  dependency.
- Concurrency. Two or more independent upstream calls in one request run
  concurrently with `errgroup` and a shared context; calls that depend on
  each other stay sequential. Every goroutine has an owner that waits for
  it and a way to stop it; none outlives its request unless question 1
  chose a pooled goroutine.
- Timeouts. Every outbound call takes a context with the deadline the
  spec states for it. Never use `context.Background()` for a call made
  while serving a request; derive the context from the request's.
- Errors. Every function that returns another function's error wraps it
  with what it was doing, using `%w`: a store or client
  (`fmt.Errorf("read labels for todo %s: %w", id, err)`), and the service
  that calls it (`fmt.Errorf("load todo details %s: %w", id, err)`). Never
  return a callee's error bare. Sentinel errors the handler matches with
  `errors.Is` stay matchable through the wrapping. Map errors to a status
  code in the handler only. Log a failure once, where it is handled.
- Kafka. A consumer is idempotent: handling a record twice has the same
  effect as once. It commits the offset only after its durable write
  succeeds. A producer sets the event's identity and timestamp itself, so
  a retry carries the same values.
- Migrations. A schema change is a new, next-numbered migration file; a
  shipped migration is never edited. Code that reads a new column ships
  in the same ticket as, or a later ticket than, the migration that adds
  it.
- Tests. One table-driven test per changed behaviour: a slice of named
  cases run with `t.Run`, covering the success path, each error path in
  question 4 and the boundary cases. Do not write one test function per
  case.
  Code that touches a real store, broker or cache also gets a test under
  the repository's integration tag. A ticket that adds a goroutine, an
  `errgroup`, a cache or shared mutable state must name `go test -race`
  on the changed packages in its tests.
- Size. A function has at most 25 decision points; the repository's own
  test enforces it where one exists. Split by responsibility instead of
  adding to a function that is already near the limit.
