# beebase-hive-service

Hive management service for [BeeBase](https://github.com/sbezhuk/beebase-auth-service#trust-model),
an open-source backend for a beekeeper management application split into
microservices. See [CLAUDE.md](https://github.com/sbezhuk/beebase-auth-service/blob/main/CLAUDE.md)
for the architectural rules this service follows.

Register/login/refresh live in `beebase-auth-service`, apiaries live in
`beebase-apiary-service` — this service only manages hives, and never
trusts a user ID or an apiary's ownership from anywhere but a verified
access token and a live check against apiary-service.

Related services: `beebase-auth-service` (users, refresh tokens, JWT
issuing), `beebase-apiary-service`, `beebase-inspection-service`,
`beebase-gateway` (single entry point for clients).

## Requirements

- Go 1.27+
- PostgreSQL 16 (or Docker, to run it for you)
- [golang-migrate](https://github.com/golang-migrate/migrate) CLI, for applying
  migrations outside Docker: `make migrate-install`
- A running `beebase-auth-service` (or anything serving a compatible
  JWKS document) reachable at `AUTH_JWKS_URL`
- A running `beebase-apiary-service` reachable at `APIARY_SERVICE_URL`

## Quick start

```bash
cp .env.example .env
# point AUTH_JWKS_URL and APIARY_SERVICE_URL at running services, e.g.
#   http://localhost:8081/.well-known/jwks.json and http://localhost:8082

# Option A: run Postgres in Docker, app on the host
docker compose up -d postgres
make migrate-up
make run

# Option B: run everything in Docker (migrations run once, automatically)
docker compose up --build
```

Verify it's up:

```bash
curl http://localhost:8080/health   # liveness — always 200 while the process is up
curl http://localhost:8080/ready    # readiness — 200 only if the database is reachable

TOKEN=...      # an access_token from auth-service's /api/v1/auth/register or /login
APIARY_ID=...  # an apiary that TOKEN's owner created via apiary-service

curl -X POST http://localhost:8080/api/v1/hives \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"apiary_id\":\"$APIARY_ID\",\"name\":\"Hive 1\"}"

curl http://localhost:8080/api/v1/hives -H "Authorization: Bearer $TOKEN"
```

The full API surface is documented in [api/openapi.yaml](api/openapi.yaml).

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
| `AUTH_JWKS_URL`             | *(required)*                 | auth-service's public key endpoint, used to verify access tokens |
| `PUBLIC_BASE_URL`           | *(required)*                 | Gateway's externally reachable base URL, used to build each image's `image_url` |
| `APIARY_SERVICE_URL`        | *(required)*                 | apiary-service's base URL, used to confirm apiary ownership on create |
| `TEST_DATABASE_URL`         | *(unset)*                    | Used only by `make test-integration`, never by the app |

## Project structure

```
cmd/server/                    entry point: wires config, logger, db, services, server
api/openapi.yaml                 API contract
migrations/                      SQL migrations (golang-migrate format)
internal/
  domain/hive/                     Hive entity, Repository port; no infrastructure dependency
  application/hive/                 use cases: create, get, list, update, delete;
                                     ApiaryVerifier port (apiary ownership check)
  platform/apiaryclient/           ApiaryVerifier implemented by calling apiary-service over HTTP
  repository/postgres/             domain port implemented against PostgreSQL (pgx, explicit SQL)
  transport/http/                 chi router, health/ready handlers
    hive/                            hive HTTP handlers, request validation, responses
```

logger, JSON response/error helpers, the graceful-shutdown server wrapper,
and JWKS-based access-token verification (`RequireAuth` middleware) all
come from [beebase-common](https://github.com/sbezhuk/beebase-common),
shared by every BeeBase service.

## Ownership

A hive belongs to exactly one apiary and, through it, one user. Ownership
is enforced in two layers:

1. **On create**, this service forwards the caller's own access token to
   apiary-service's `GET /api/v1/apiaries/{apiaryId}` and trusts its
   answer: a 200 means whoever holds that token owns that apiary, a 404
   means they don't (or it doesn't exist) — collapsed into the same
   `404 apiary_not_found` response either way, so an apiary's existence
   can't be probed. This service never queries apiary ownership itself.
2. The verified owner's user ID is then denormalized onto the hive row.
   Every later read/write (`GetByID`, `Update`, `Delete`) scopes its SQL
   by that `user_id` directly, exactly like apiary-service scopes
   apiaries — no cross-service call is needed after creation, since
   `apiary_id` is immutable and ownership can't change out from under a
   hive. A request for another user's hive returns the same
   `404 hive_not_found` as one that doesn't exist, never a `403`.

Deletes are soft (`deleted_at` is set, the row is retained) per the
project's offline-sync plan — hives are a synchronizable entity.

**Known limitation:** if an apiary is deleted in apiary-service, its
hives here are not cascade-deleted or notified — there's no event bus or
outbox between services yet (CLAUDE.md defers full synchronization).
Those hives become orphaned but remain independently accessible to their
owner until this is addressed.

## Development

```bash
make run               # go run ./cmd/server
make fmt                # go fmt ./...
make vet                # go vet ./...
make test               # unit tests: go test ./...
make lint                # golangci-lint run

make migrate-up         # apply migrations to DATABASE_URL
make migrate-down       # roll back the last migration
make migrate-new name=add_something   # scaffold a new migration pair

make build              # build binary into bin/
```

### Integration tests

Integration tests exercise the PostgreSQL repository and the full HTTP
CRUD flow — including a real JWKS round trip, a fake apiary-service
standing in for the real cross-service ownership check, and two
independently authenticated users proving cross-user access is
impossible — against a real database. They're gated on
`TEST_DATABASE_URL` and skip themselves (not fail) if it's unset, and
every test runs inside a transaction that's rolled back afterward, so
they never leave rows behind or need manual cleanup.

```bash
docker compose up -d postgres
createdb -h localhost -p 5434 -U beebase beebase_hive_test
migrate -path migrations -database "$TEST_DATABASE_URL" up

TEST_DATABASE_URL=postgres://beebase:beebase@localhost:5434/beebase_hive_test?sslmode=disable \
  make test-integration
```
