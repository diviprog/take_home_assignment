package discovery

import (
	"fmt"
	"sort"
)

// Stitch reassembles items that run across page breaks. Pages were extracted
// independently (so they can run in parallel and cache individually); this is
// the deterministic, order-dependent step that undoes the page chunking:
// leading continuation text is appended to the previous item's description,
// and the item's page range is extended. Section boundaries are hard
// barriers — the six scans were stapled together, so text at the top of a
// section-start page cannot belong to the previous section's last item; it is
// reported as a warning instead of being silently glued or dropped.
func Stitch(pages []PageExtract) ([]StitchedItem, []string) {
	sort.Slice(pages, func(i, j int) bool { return pages[i].PdfPage < pages[j].PdfPage })

	var items []StitchedItem
	var warnings []string
	for _, page := range pages {
		if lead := page.LeadingContinuationText; lead != "" {
			switch {
			case SectionStarts[page.PdfPage]:
				warnings = append(warnings, fmt.Sprintf(
					"page %d: leading text on a section-start page, not stitched: %.80q", page.PdfPage, lead))
			case len(items) == 0:
				warnings = append(warnings, fmt.Sprintf(
					"page %d: leading text with no previous item to attach to: %.80q", page.PdfPage, lead))
			default:
				last := &items[len(items)-1]
				last.Description += "\n\n" + lead
				last.PdfPageEnd = page.PdfPage
			}
		}
		for _, it := range page.Items {
			items = append(items, StitchedItem{RawItem: it, PdfPageStart: page.PdfPage, PdfPageEnd: page.PdfPage})
		}
	}
	return items, warnings
}
