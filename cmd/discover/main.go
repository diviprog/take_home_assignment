// Command discover runs Pass A of the extraction: an open-vocabulary survey
// of data/items_combined.pdf. It renders each page, has a vision model
// transcribe it verbatim into structured JSON (cached on disk, so re-runs are
// free), stitches items that cross page breaks, and writes:
//
//	notes/discovery/items.json — every item, stitched, source-verbatim
//	notes/discovery/tally.md   — distinct-value counts per field
//
// The tally is the evidence for freezing the ontology's enums and synonym
// folds; Pass B (cmd/extract) then re-extracts strictly against that frozen
// vocabulary and writes to Postgres. This command never touches the database.
//
//	go run ./cmd/discover            # all 39 pages
//	go run ./cmd/discover -pages 1-3 # smoke test on a slice
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"encoding/json"

	"oddities/internal/discovery"
)

const (
	pdfPath   = "data/items_combined.pdf"
	pageCount = 39
)

func main() {
	pagesFlag := flag.String("pages", fmt.Sprintf("1-%d", pageCount), "page range to process, e.g. 5-9")
	dpi := flag.Int("dpi", 150, "render resolution")
	workers := flag.Int("workers", 4, "concurrent API calls")
	refresh := flag.Bool("refresh", false, "ignore the response cache and re-call the API")
	backend := flag.String("backend", "cli", "how to reach the model: cli (local claude login / subscription) or api (metered ANTHROPIC_API_KEY)")
	flag.Parse()

	first, last, err := parseRange(*pagesFlag)
	if err != nil {
		fatal("bad -pages: %v", err)
	}

	ctx := context.Background()

	pageFiles, err := discovery.RenderPages(pdfPath, filepath.Join("cache", "pages"), first, last, *dpi)
	if err != nil {
		fatal("render: %v", err)
	}
	fmt.Printf("rendered pages %d-%d\n", first, last)

	ex, err := discovery.NewExtractor(filepath.Join("cache", "discovery"), *backend)
	if err != nil {
		fatal("extractor: %v", err)
	}

	// Fan the pages out over a small worker pool. Each page is independent —
	// order is restored later by the stitcher — so failures are collected per
	// page instead of aborting the run.
	type result struct {
		page    int
		extract discovery.PageExtract
		cached  bool
		err     error
	}
	jobs := make(chan int)
	results := make(chan result)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				extract, cached, err := ex.ExtractPage(ctx, p, pageFiles[p], *refresh)
				results <- result{page: p, extract: extract, cached: cached, err: err}
			}
		}()
	}
	go func() {
		for p := first; p <= last; p++ {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var pages []discovery.PageExtract
	var failed []string
	cachedCount, apiCount := 0, 0
	for r := range results {
		if r.err != nil {
			failed = append(failed, r.err.Error())
			fmt.Printf("  page %2d FAILED: %v\n", r.page, r.err)
			continue
		}
		if r.cached {
			cachedCount++
		} else {
			apiCount++
		}
		src := "live"
		if r.cached {
			src = "cache"
		}
		fmt.Printf("  page %2d ok (%5s): %d items, continuation=%v\n",
			r.page, src, len(r.extract.Items), r.extract.LeadingContinuationText != "")
		pages = append(pages, r.extract)
	}

	items, warnings := discovery.Stitch(pages)
	if items == nil {
		items = []discovery.StitchedItem{} // marshal as [], not null
	}

	// Deep-label pass: items whose prose crossed a page break were labeled
	// only from their opening page (all four artifacts open with pure lore),
	// so their label fields miss every mechanic on later pages. Re-derive the
	// labels from each such item's complete stitched text. Cached like the
	// page pass; single-page items are untouched.
	rl, err := discovery.NewRelabeler(filepath.Join("cache", "relabel"), *backend)
	if err != nil {
		fatal("relabeler: %v", err)
	}
	for i := range items {
		if !discovery.NeedsRelabel(items[i]) {
			continue
		}
		labels, cached, err := rl.Relabel(ctx, items[i], *refresh)
		if err != nil {
			failed = append(failed, err.Error())
			fmt.Printf("  relabel %-28s FAILED: %v\n", items[i].Name, err)
			continue
		}
		src := "live"
		if cached {
			src = "cache"
		}
		labels.Merge(&items[i])
		fmt.Printf("  relabel %-28s ok (%5s): %d effects, %d limits\n",
			items[i].Name, src, len(items[i].EffectKindsRaw), len(items[i].LimitationsRaw))
	}

	outDir := filepath.Join("notes", "discovery")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal("mkdir %s: %v", outDir, err)
	}
	itemsJSON, _ := json.MarshalIndent(items, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, "items.json"), itemsJSON, 0o644); err != nil {
		fatal("write items.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "tally.md"), []byte(discovery.Tally(items)), 0o644); err != nil {
		fatal("write tally.md: %v", err)
	}

	// Reconciliation: every page and every warning accounted for, nothing
	// silently dropped.
	fmt.Printf("\n%d pages processed (%d cached, %d live), %d failed\n",
		len(pages), cachedCount, apiCount, len(failed))
	fmt.Printf("%d items after stitching -> %s\n", len(items), filepath.Join(outDir, "items.json"))
	fmt.Printf("tally -> %s\n", filepath.Join(outDir, "tally.md"))
	sort.Strings(warnings)
	for _, w := range warnings {
		fmt.Println("stitch warning:", w)
	}
	for _, f := range failed {
		fmt.Println("page failure:", f)
	}
	if len(failed) > 0 {
		os.Exit(1)
	}
}

func parseRange(s string) (int, int, error) {
	var first, last int
	if _, err := fmt.Sscanf(s, "%d-%d", &first, &last); err != nil {
		if _, err := fmt.Sscanf(s, "%d", &first); err != nil {
			return 0, 0, fmt.Errorf("want N or N-M, got %q", s)
		}
		last = first
	}
	if first < 1 || last > pageCount || first > last {
		return 0, 0, fmt.Errorf("range %q outside 1-%d", s, pageCount)
	}
	return first, last, nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
