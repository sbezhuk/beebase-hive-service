# beebase-hive-service

Hive management service for [BeeBase](https://github.com/sbezhuk/beebase-auth-service#trust-model),
an open-source backend for a beekeeper management application split into
microservices. See [CLAUDE.md](https://github.com/sbezhuk/beebase-auth-service/blob/main/CLAUDE.md)
for the architectural rules this service follows.

This repository currently contains only the **project foundation**:
configuration, HTTP server, PostgreSQL connection, structured logging,
graceful shutdown, and health/readiness endpoints — the same starting
point [beebase-auth-service](https://github.com/sbezhuk/beebase-auth-service)
had before its first feature was built. Hive CRUD isn't implemented yet;
when it is, this service will verify access tokens issued by auth-service
via [beebase-common](https://github.com/sbezhuk/beebase-common)'s
JWKS-based middleware, the same way every other BeeBase service does.

Related services: `beebase-auth-service` (users, refresh tokens, JWT
issuing), `beebase-apiary-service`, `beebase-inspection-service`,
`beebase-gateway` (single entry point for clients).

## Requirements

- Go 1.27+
- PostgreSQL 16 (or Docker, to run it for you)
- [golang-migrate](https://github.com/golang-migrate/migrate) CLI, for applying
  migrations outside Docker: `make migrate-install`

## Quick start

```bash
cp .env.example .env

# Option A: run Postgres in Docker, app on the host
docker compose up -d postgres
make run

# Option B: run everything in Docker
docker compose up --build
```

Verify it's up:

```bash
curl http://localhost:8080/health   # liveness — always 200 while the process is up
curl http://localhost:8080/ready    # readiness — 200 only if the database is reachable
```

Note: this repo's `docker-compose.yml` is for standalone single-service
development only. To run the full BeeBase stack together, use
`beebase-gateway`'s docker-compose, which builds every service from
sibling checkouts and routes between them.

## Configuration

All configuration is via environment variables (see
[.env.example](.env.example)):

| Variable                   | Default                    | Description                              |
| --------------------------- | --------------------------- | ----------------------------------------- |
| `APP_ENV`                  | `development`               | `development` or `production`             |
| `LOG_LEVEL`                 | `info`                       | `debug`, `info`, `warn`, `error`           |
| `HTTP_PORT`                 | `8080`                       | Port the HTTP server listens on           |
| `HTTP_READ_TIMEOUT`         | `5s`                         | Request read timeout                      |
| `HTTP_WRITE_TIMEOUT`        | `10s`                        | Response write timeout                    |
| `HTTP_IDLE_TIMEOUT`         | `60s`                        | Keep-alive idle timeout                   |
| `HTTP_SHUTDOWN_TIMEOUT`     | `15s`                        | Max time to wait for graceful shutdown    |
| `DATABASE_URL`              | *(required)*                 | PostgreSQL DSN                            |
| `DATABASE_CONNECT_TIMEOUT`  | `5s`                         | Timeout for the initial DB connection      |
| `TEST_DATABASE_URL`         | *(unset)*                    | Used only by `make test-integration`, never by the app |

## Project structure

```
cmd/server/              entry point: wires config, logger, db, server
api/                     (not yet added) API contract, once endpoints exist
migrations/              SQL migrations (golang-migrate format, empty for now)
internal/
  config/                 environment-based configuration
  platform/
    postgres/               pgx connection pool
  transport/http/          chi router, health/ready handlers
```

logger, JSON response/error helpers, the graceful-shutdown server wrapper,
and access-token verification (once there's an endpoint to protect) all
come from [beebase-common](https://github.com/sbezhuk/beebase-common),
shared by every BeeBase service.

## Development

```bash
make run     # go run ./cmd/server
make fmt     # go fmt ./...
make vet     # go vet ./...
make test    # go test ./...
make build   # build binary into bin/
```
