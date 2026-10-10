# AGENTS.md

A Go todo service backed by Postgres, Kafka and Redis. `README.md` has the
layout, `ARCHITECTURE.md` the design and `docs/design-guide/go-service.md`
the team's rules for specs and plans.

- Setup: none for unit tests. The integration tests need the Postgres,
  Kafka and Redis services of `docker-compose.yml`.
- Build: `go build ./...`
- Test: `make test` (unit tests, no services needed)
- Full suite: `make verify-integration` against services that are already
  running; `make verify` starts and stops them itself with Docker.
- Lint: `go vet ./...`

Integration tests carry the `integration` build tag and run with `-p 1`:
packages share one `todos` table.
