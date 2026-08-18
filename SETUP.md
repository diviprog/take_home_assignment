# Setup

## Prerequisites

- Go 1.26.2+
- Docker (for the local Postgres)
- [`sqlc`](https://docs.sqlc.dev/en/latest/overview/install.html) — `brew install sqlc`

## 1. Start Postgres

```bash
docker compose up -d
```

One pinned PostgreSQL 18 (its built-in `uuidv7()` backs the `object` base class),
reachable at `postgres://postgres:postgres@localhost:5433/postgres`. Override with
`DATABASE_URL` if you need to. Reset the database at any time with:

```bash
docker compose down -v && docker compose up -d
```

## 2. Verify

```bash
go run ./cmd/verify
```

You should see `postgres is ready`.

## 3. Run the extraction

Use the locally authenticated Claude CLI:

```bash
go run ./cmd/extract -backend cli
```

Or set `ANTHROPIC_API_KEY` and run with `-backend api`. The committed discovery
artifact avoids repeating the PDF vision pass; normalization calls are cached
locally after the first run.

After a schema or query change, regenerate typed Go from `database/`:

```bash
cd database && sqlc generate
```

## Layout

```
compose.yaml            local Postgres (port 5433)
db/                     connection pool + declarative-schema provisioner
database/
  schema/foundation.sql base `object` class + touch trigger (provided)
  schema/               your entity types go here
  query/                your named queries for sqlc
  generated/            sqlc output (typed Go)
  sqlc.yaml
cmd/verify              "postgres is ready" check
cmd/extract             extraction pipeline
data/items_combined.pdf the source catalog
```
