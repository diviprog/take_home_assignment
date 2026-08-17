package discovery

import "testing"

func page(pdfPage int, leading string, names ...string) PageExtract {
	p := PageExtract{PdfPage: pdfPage, LeadingContinuationText: leading}
	for _, n := range names {
		p.Items = append(p.Items, RawItem{Name: n, Description: n + " body"})
	}
	return p
}

// The stitcher is the one order-dependent, hand-written piece of Pass A, so
// it gets the unit coverage: continuation gluing, multi-page runs, section
// barriers, and out-of-order input (pages arrive from a worker pool).
func TestStitchGluesContinuationAcrossPageBreak(t *testing.T) {
	items, warnings := Stitch([]PageExtract{
		page(2, "", "Amulet"),
		page(3, "rest of the amulet text.", "Cloak"),
	})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if want := "Amulet body\n\nrest of the amulet text."; items[0].Description != want {
		t.Errorf("description = %q, want %q", items[0].Description, want)
	}
	if items[0].PdfPageStart != 2 || items[0].PdfPageEnd != 3 {
		t.Errorf("pages = %d-%d, want 2-3", items[0].PdfPageStart, items[0].PdfPageEnd)
	}
}

func TestStitchHandlesWholePageContinuation(t *testing.T) {
	// Page 39 of the real PDF: pure continuation prose, no new items.
	items, warnings := Stitch([]PageExtract{
		page(38, "", "Amulet of the Ancients"),
		page(39, "Immunities. The wearer is immune..."),
	})
	if len(warnings) != 0 || len(items) != 1 {
		t.Fatalf("items=%d warnings=%v", len(items), warnings)
	}
	if items[0].PdfPageEnd != 39 {
		t.Errorf("PdfPageEnd = %d, want 39", items[0].PdfPageEnd)
	}
}

func TestStitchNeverCrossesSectionBoundary(t *testing.T) {
	// Leading text on a section-start page cannot belong to the previous
	// section's last item; it must surface as a warning, not a glue.
	items, warnings := Stitch([]PageExtract{
		page(11, "", "Shield"),
		page(12, "stray text at top of new section", "Cold Flame"),
	})
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].Description != "Shield body" {
		t.Errorf("section-1 item was mutated: %q", items[0].Description)
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning, got %v", warnings)
	}
}

func TestStitchSortsPagesFromParallelWorkers(t *testing.T) {
	items, warnings := Stitch([]PageExtract{
		page(3, "tail of sword text."),
		page(2, "", "Sword"),
	})
	if len(warnings) != 0 || len(items) != 1 {
		t.Fatalf("items=%d warnings=%v", len(items), warnings)
	}
	if want := "Sword body\n\ntail of sword text."; items[0].Description != want {
		t.Errorf("description = %q, want %q", items[0].Description, want)
	}
}
