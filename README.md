# Reznar's Arcane Oddities

This submission models the 80 magic items in `data/items_combined.pdf` as a
typed PostgreSQL ontology and provides an AI-driven Go pipeline that extracts,
normalizes, validates, and inserts the catalog.

## Results

- 80 of 80 items inserted; zero quarantined.
- 36 wondrous items, 21 weapons, 14 armor, 5 rings, and 4 potions.
- 220 effect tags, 146 limitation clauses, 42 creature targets, 26 variants,
  19 restricted-attunement links, and 31 spell links.
- `go test ./...` and `go vet ./...` pass.

The complete reconciliation output is committed at
`notes/extraction/report.md`.

## Run it

Prerequisites: Go 1.26.2+, Docker, Poppler (`pdftoppm`), and either the Claude CLI
with an active login or an Anthropic API key. `sqlc` is needed only after
changing the schema; generated Go is committed.

```bash
docker compose up -d
go run ./cmd/verify
go run ./cmd/extract -backend cli
```

For API authentication, set `ANTHROPIC_API_KEY` and use `-backend api`.
Extraction requires an empty database because the declarative schema is
provisioned as a unit. To rerun locally:

```bash
docker compose down -v
docker compose up -d
go run ./cmd/extract -backend cli
```

The committed Pass A survey at `notes/discovery/items.json` avoids repeating
the vision pass. Model responses are otherwise cached locally by their inputs
and excluded from Git. Use `-refresh` to bypass the normalization cache, or
remove the survey artifact to exercise the full PDF-to-database path.

## Design

The pipeline deliberately separates observation from normalization:

1. `cmd/discover` renders the scanned PDF, extracts one page at a time into an
   open vocabulary, stitches page continuations, and re-labels multi-page
   items from their complete text.
2. `cmd/extract` normalizes each discovered item into the frozen ontology,
   validates the result in Go, and inserts it through sqlc-generated queries.
3. PostgreSQL constraints provide the final integrity boundary. Each item is
   inserted transactionally; failures are quarantined instead of partially
   loading or being coerced into a nearby value.

The schema is in `database/schema/`, with one file per object type and typed
vocabulary in `types.sql`. The observation-to-implementation rationale for
each modeled dimension is in `notes/decisions.md`.

## Repository map

```text
cmd/discover/               open-vocabulary PDF survey
cmd/extract/                normalization, validation, and database load
cmd/verify/                 database connectivity check
database/schema/            declarative PostgreSQL ontology
database/query/             sqlc queries
database/generated/         committed generated Go
internal/discovery/         vision extraction, stitching, and survey logic
internal/extraction/        normalization, validation, and insertion logic
notes/discovery/items.json  committed 80-item Pass A result
notes/discovery/tally.md     observations used to choose the vocabulary
notes/extraction/report.md  final reconciliation and coverage report
```

## Verification

```bash
go test ./...
go vet ./...
cd database && sqlc diff
```

`sqlc diff` verifies that committed generated code matches the schema and
queries without rewriting files.
