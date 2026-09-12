.PHONY: test verify

# Fast unit tests only -- the in-memory Store with the noop publisher, no
# Docker required.
test:
	go test ./...

# Full suite: brings up the docker-compose Postgres and Kafka dependencies,
# waits for both to be healthy, runs the Postgres-backed integration
# tests against them, then tears everything down. This is the target a
# sandboxed run's compose-services provisioner needs to be able to
# satisfy without the worker ever holding a Postgres/Kafka credential
# itself -- see docker-compose.yml.
verify: test
	docker compose up -d --wait postgres kafka
	TODO_DATABASE_URL="postgres://todo:todo@localhost:5433/todo?sslmode=disable" \
		go test -tags=integration ./... ; \
		status=$$?; \
		docker compose down -v; \
		exit $$status
