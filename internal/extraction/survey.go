package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"oddities/internal/discovery"
)

// EnsureSurvey returns the Pass A survey (stitched, deep-relabeled, verbatim
// items). If cmd/discover already ran, its artifact is loaded as-is — the
// survey a human reviewed is the survey Pass B consumes. On a fresh clone the
// full Pass A chain runs here instead (render → per-page extract → stitch →
// relabel), against the same disk caches, so `go run ./cmd/extract` is the
// one command a reviewer needs.
func EnsureSurvey(ctx context.Context, pdfPath string, pages, workers int, backend string, refresh bool) ([]discovery.StitchedItem, error) {
	itemsPath := filepath.Join("notes", "discovery", "items.json")
	if raw, err := os.ReadFile(itemsPath); err == nil && !refresh {
		var items []discovery.StitchedItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("parse %s: %w", itemsPath, err)
		}
		fmt.Printf("survey: %d items from %s\n", len(items), itemsPath)
		return items, nil
	}

	fmt.Printf("survey: %s missing — running Pass A over %d pages\n", itemsPath, pages)
	pageFiles, err := discovery.RenderPages(pdfPath, filepath.Join("cache", "pages"), 1, pages, 150)
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	ex, err := discovery.NewExtractor(filepath.Join("cache", "discovery"), backend)
	if err != nil {
		return nil, err
	}

	type result struct {
		extract discovery.PageExtract
		err     error
	}
	jobs := make(chan int)
	results := make(chan result)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				extract, _, err := ex.ExtractPage(ctx, p, pageFiles[p], refresh)
				results <- result{extract, err}
			}
		}()
	}
	go func() {
		for p := 1; p <= pages; p++ {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var extracts []discovery.PageExtract
	for r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("survey page: %w", r.err)
		}
		extracts = append(extracts, r.extract)
	}

	items, warnings := discovery.Stitch(extracts)
	for _, w := range warnings {
		fmt.Println("survey stitch warning:", w)
	}

	rl, err := discovery.NewRelabeler(filepath.Join("cache", "relabel"), backend)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if !discovery.NeedsRelabel(items[i]) {
			continue
		}
		labels, _, err := rl.Relabel(ctx, items[i], refresh)
		if err != nil {
			return nil, err
		}
		labels.Merge(&items[i])
	}

	// Persist the artifacts exactly as cmd/discover would, so the two entry
	// points can never disagree about what the survey said.
	outDir := filepath.Join("notes", "discovery")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	itemsJSON, _ := json.MarshalIndent(items, "", "  ")
	if err := os.WriteFile(itemsPath, itemsJSON, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "tally.md"), []byte(discovery.Tally(items)), 0o644); err != nil {
		return nil, err
	}
	fmt.Printf("survey: %d items -> %s\n", len(items), itemsPath)
	return items, nil
}
