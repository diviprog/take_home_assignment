// Command extract is Pass B, the pipeline the assignment asks for: it reads
// the catalog PDF and populates the database against the ontology in
// database/schema.
//
// It builds on the Pass A survey (cmd/discover — run here automatically if
// its artifact is missing): every stitched, verbatim item is normalized into
// the frozen vocabulary by a model call whose output schema only admits
// frozen enum members, validated mechanically against the same vocabulary and
// the schema's shape constraints, and inserted through the sqlc-generated
// queries in one transaction per item. Items that fail validation or
// insertion are QUARANTINED — reported with reasons, never bent to fit — and
// the run ends with a reconciliation: every discovered item accounted for as
// inserted or quarantined, plus per-table row counts from the database
// itself.
//
//	go run ./cmd/extract              # fresh database (see SETUP.md)
//	go run ./cmd/extract -refresh     # ignore the normalize cache
//
// Artifacts: notes/extraction/report.md (reconciliation + quarantine detail).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"oddities/database"
	"oddities/database/generated"
	"oddities/db"
	"oddities/internal/extraction"
)

const (
	pdfPath   = "data/items_combined.pdf"
	pageCount = 39
)

func main() {
	workers := flag.Int("workers", 4, "concurrent model calls")
	refresh := flag.Bool("refresh", false, "ignore the normalize cache and re-call the model")
	backend := flag.String("backend", "cli", "how to reach the model: cli (local claude login / subscription) or api (metered ANTHROPIC_API_KEY)")
	limit := flag.Int("limit", 0, "process only the first N items (0 = all) — smoke-test the pipeline before a full run")
	flag.Parse()

	ctx := context.Background()

	pool, err := db.Connect(ctx)
	if err != nil {
		fatal("connect: %v", err)
	}
	defer pool.Close()

	// Provision the ontology from database/schema/*.sql into the empty
	// database (fresh cluster required: docker compose down -v && up -d).
	if err := db.Apply(ctx, pool, database.Schema()); err != nil {
		fatal("provision schema (needs an empty database — docker compose down -v && docker compose up -d): %v", err)
	}

	if _, err := os.Stat(pdfPath); err != nil {
		fatal("open %s: %v", pdfPath, err)
	}

	// Pass A survey: loaded from cmd/discover's artifact, or run here.
	items, err := extraction.EnsureSurvey(ctx, pdfPath, pageCount, *workers, *backend, false)
	if err != nil {
		fatal("survey: %v", err)
	}
	if *limit > 0 && *limit < len(items) {
		fmt.Printf("smoke run: limiting to the first %d of %d items\n", *limit, len(items))
		items = items[:*limit]
	}

	norm, err := extraction.NewNormalizer(filepath.Join("cache", "normalize"), *backend)
	if err != nil {
		fatal("normalizer: %v", err)
	}

	// Normalize every item concurrently (each call is independent and
	// disk-cached); insert sequentially afterwards so the write path stays a
	// readable straight line and failures attribute cleanly.
	type outcome struct {
		idx        int
		normalized extraction.NormalizedItem
		cached     bool
		err        error
	}
	jobs := make(chan int)
	results := make(chan outcome)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				n, cached, err := norm.Normalize(ctx, items[i], *refresh)
				results <- outcome{idx: i, normalized: n, cached: cached, err: err}
			}
		}()
	}
	go func() {
		for i := range items {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	normalized := make([]extraction.NormalizedItem, len(items))
	type quarantined struct {
		Name    string   `json:"name"`
		Stage   string   `json:"stage"`
		Reasons []string `json:"reasons"`
	}
	var quarantine []quarantined
	failed := map[int]bool{}
	cachedCount, liveCount := 0, 0
	for r := range results {
		name := items[r.idx].Name
		if r.err != nil {
			quarantine = append(quarantine, quarantined{Name: name, Stage: "normalize", Reasons: []string{r.err.Error()}})
			failed[r.idx] = true
			fmt.Printf("  normalize %-32s FAILED: %v\n", name, r.err)
			continue
		}
		if r.cached {
			cachedCount++
		} else {
			liveCount++
		}
		normalized[r.idx] = r.normalized
	}
	fmt.Printf("normalized %d items (%d cached, %d live), %d failed\n",
		len(items)-len(failed), cachedCount, liveCount, len(failed))

	// Validate + insert, one transaction per item: a constraint violation
	// quarantines that item alone and the rest of the catalog still lands.
	inserted := 0
	var totals extraction.InsertCounts
	var unplaced []string
	for i, it := range items {
		if failed[i] {
			continue
		}
		n := normalized[i]
		n.Dedupe()
		if errs := extraction.Validate(it, n); len(errs) > 0 {
			quarantine = append(quarantine, quarantined{Name: it.Name, Stage: "validate", Reasons: errs})
			fmt.Printf("  quarantine %-30s %d validation error(s)\n", it.Name, len(errs))
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			fatal("begin: %v", err)
		}
		counts, err := extraction.Insert(ctx, tx, it, n)
		if err != nil {
			tx.Rollback(ctx)
			quarantine = append(quarantine, quarantined{Name: it.Name, Stage: "insert", Reasons: []string{err.Error()}})
			fmt.Printf("  quarantine %-30s insert: %v\n", it.Name, err)
			continue
		}
		if err := tx.Commit(ctx); err != nil {
			fatal("commit %q: %v", it.Name, err)
		}
		inserted++
		totals.Variants += counts.Variants
		totals.Attuners += counts.Attuners
		totals.Effects += counts.Effects
		totals.Targets += counts.Targets
		totals.Limitations += counts.Limitations
		totals.Spells += counts.Spells
		for _, u := range n.Unplaced {
			unplaced = append(unplaced, fmt.Sprintf("%s: %s", it.Name, u))
		}
	}

	// Reconciliation: the database's own counts close the loop — what the
	// pipeline claims to have inserted is re-read from Postgres, and every
	// discovered item is accounted for as inserted or quarantined.
	byCategory, err := generated.New(pool).CountItemsByCategory(ctx)
	if err != nil {
		fatal("count by category: %v", err)
	}
	dbTotal := 0
	for _, row := range byCategory {
		dbTotal += int(row.Items)
	}

	var b strings.Builder
	b.WriteString("# Extraction report (Pass B)\n\n")
	fmt.Fprintf(&b, "%d items discovered; %d inserted, %d quarantined; database holds %d item rows.\n",
		len(items), inserted, len(quarantine), dbTotal)
	fmt.Fprintf(&b, "\nChild rows: %d variants, %d attunement allowlist, %d effect tags, %d creature targets, %d limitations, %d spell links.\n",
		totals.Variants, totals.Attuners, totals.Effects, totals.Targets, totals.Limitations, totals.Spells)
	b.WriteString("\n## Items by category (from the database)\n\n")
	for _, row := range byCategory {
		fmt.Fprintf(&b, "- %3d × %s\n", row.Items, row.Category)
	}
	if len(quarantine) > 0 {
		b.WriteString("\n## Quarantine\n\nItems that did not land, with reasons; each needs a human ruling.\n")
		for _, qi := range quarantine {
			fmt.Fprintf(&b, "\n### %s (%s)\n", qi.Name, qi.Stage)
			for _, r := range qi.Reasons {
				fmt.Fprintf(&b, "- %s\n", r)
			}
		}
	}
	if len(unplaced) > 0 {
		b.WriteString("\n## Unplaced facts\n\nMechanics the model found no vocabulary home for — candidates for growing the ontology.\n\n")
		sort.Strings(unplaced)
		for _, u := range unplaced {
			fmt.Fprintf(&b, "- %s\n", u)
		}
	}

	outDir := filepath.Join("notes", "extraction")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal("mkdir %s: %v", outDir, err)
	}
	reportPath := filepath.Join(outDir, "report.md")
	if err := os.WriteFile(reportPath, []byte(b.String()), 0o644); err != nil {
		fatal("write report: %v", err)
	}
	if len(quarantine) > 0 {
		qJSON, _ := json.MarshalIndent(quarantine, "", "  ")
		if err := os.WriteFile(filepath.Join(outDir, "quarantine.json"), qJSON, 0o644); err != nil {
			fatal("write quarantine: %v", err)
		}
	}

	fmt.Printf("\n%d/%d items inserted (%d quarantined) -> %s\n", inserted, len(items), len(quarantine), reportPath)
	if inserted+len(quarantine) != len(items) {
		fatal("reconciliation failure: %d inserted + %d quarantined != %d discovered", inserted, len(quarantine), len(items))
	}
	if dbTotal != inserted {
		fatal("reconciliation failure: database holds %d items but pipeline inserted %d", dbTotal, inserted)
	}
	if len(quarantine) > 0 {
		os.Exit(1)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
