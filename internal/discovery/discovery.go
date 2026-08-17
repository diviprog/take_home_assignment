// Package discovery is Pass A of a two-pass extraction: an open-vocabulary
// survey of the catalog PDF. It does NOT write to the database. It renders
// each page, asks a vision model to transcribe the items on it using the
// source's own words (no normalization, no invented categories), stitches
// multi-page items back together, and tallies the distinct values seen in
// each field. The tally is the evidence we use to freeze the real enums and
// synonym folds before Pass B (the strict extraction in cmd/extract).
package discovery

// PageExtract is what the model returns for one rendered PDF page. The PDF is
// six separately-paginated scans stapled together, so an item can run across
// a page break: any prose at the top of a page that belongs to the previous
// page's last item lands in LeadingContinuationText, and the stitcher glues
// it back on. PdfPage is filled in by us, not the model — the printed page
// number on the scan restarts mid-document and cannot be trusted as an index.
type PageExtract struct {
	PdfPage                 int       `json:"pdf_page"`
	PrintedPageNumber       string    `json:"printed_page_number"`
	LeadingContinuationText string    `json:"leading_continuation_text"`
	LastItemContinues       bool      `json:"last_item_continues"`
	Items                   []RawItem `json:"items"`
}

// RawItem is one catalog entry exactly as the source states it. Every *_raw
// field is verbatim — typos like "Wonderous" included — because Pass A's job
// is to measure the source's vocabulary, not to clean it. Cleaning rules we
// haven't derived from data yet would just be guesses.
type RawItem struct {
	// Name is the item's title as printed (small caps in the source).
	Name string `json:"name"`
	// TypeLine is the italic line under the title, verbatim,
	// e.g. "Weapon (any sword), very rare" or "Wonderous item, uncommon".
	TypeLine string `json:"type_line"`
	// CategoryRaw/SubtypeRaw/RarityRaw/AttunementRaw are the type line pulled
	// apart but still in the source's words. Empty string = not stated.
	CategoryRaw   string `json:"category_raw"`
	SubtypeRaw    string `json:"subtype_raw"`
	RarityRaw     string `json:"rarity_raw"`
	AttunementRaw string `json:"attunement_raw"`
	// WornOrHeldRaw is the source's own noun/phrase for where the item is
	// worn or held ("helm", "worn about the neck"), only if the text says so.
	WornOrHeldRaw string `json:"worn_or_held_raw"`
	// Description is the full body prose, verbatim, including any sub-headed
	// paragraphs and tables that belong to this item.
	Description string `json:"description"`
	// EffectKindsRaw are the model's short free-form labels for what the item
	// does ("damage bonus", "fear on others"). Open vocabulary on purpose:
	// the tally over all pages tells us which effect families actually exist.
	EffectKindsRaw []string `json:"effect_kinds_raw"`
	// CreaturesMentioned / EnvironmentsMentioned / SpellsMentioned are
	// verbatim nouns the text ties the item to (targeting, not flavor).
	CreaturesMentioned    []string `json:"creatures_mentioned"`
	EnvironmentsMentioned []string `json:"environments_mentioned"`
	SpellsMentioned       []string `json:"spells_mentioned"`
	// LimitationsRaw are verbatim usage limits ("three times before a long
	// rest", "until the next dawn").
	LimitationsRaw []string `json:"limitations_raw"`
	// ChargesText is the charges mechanic sentence(s) verbatim, if any.
	ChargesText string `json:"charges_text"`
	// CursedText is the curse wording verbatim, if any.
	CursedText string `json:"cursed_text"`
	// VariantTableText notes any variant table (e.g. gemstone -> emotion) —
	// the dimension it varies over and the rows, compactly.
	VariantTableText string `json:"variant_table_text"`
	// Oddities is the escape hatch: anything notable that fits none of the
	// fields above. High-frequency oddities become schema in Pass B.
	Oddities []string `json:"oddities"`
}

// StitchedItem is a RawItem plus where it physically lives in the PDF after
// continuation text has been glued back on.
type StitchedItem struct {
	RawItem
	PdfPageStart int `json:"pdf_page_start"`
	PdfPageEnd   int `json:"pdf_page_end"`
}

// SectionStarts are the PDF pages where a new stapled-in scan begins (found
// by eyeballing the boundaries; each restarts its printed numbering). The
// stitcher never carries text across these — an item cannot straddle two
// different source documents.
var SectionStarts = map[int]bool{1: true, 6: true, 12: true, 18: true, 25: true, 32: true}
