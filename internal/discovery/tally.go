package discovery

import (
	"fmt"
	"sort"
	"strings"
)

// Tally counts the distinct values every item field took across the whole
// catalog. This is the profiling artifact the ontology is derived from: a
// value that shows up 40 times is an enum member, a value that shows up once
// is either a genuine outlier or a typo, and the difference is now visible
// instead of guessed. (This is "profile before you transform" folded into the
// extraction itself, since the source had to pass through a model anyway.)
func Tally(items []StitchedItem) string {
	var b strings.Builder
	b.WriteString("# Discovery tally\n\n")
	b.WriteString(fmt.Sprintf("%d items after stitching. Every value below is the source's own wording\n", len(items)))
	b.WriteString("(or, for effect kinds, the model's free labels); counts are item counts.\n")

	scalar := func(title string, get func(RawItem) string) {
		counts := map[string]int{}
		for _, it := range items {
			counts[strings.TrimSpace(get(it.RawItem))]++
		}
		writeSection(&b, title, counts)
	}
	list := func(title string, get func(RawItem) []string) {
		counts := map[string]int{}
		for _, it := range items {
			vals := get(it.RawItem)
			if len(vals) == 0 {
				counts[""]++
			}
			for _, v := range vals {
				counts[strings.TrimSpace(v)]++
			}
		}
		writeSection(&b, title, counts)
	}
	// Long free-text fields tally presence, not the text itself.
	presence := func(title string, get func(RawItem) string) {
		counts := map[string]int{}
		for _, it := range items {
			if strings.TrimSpace(get(it.RawItem)) != "" {
				counts["present"]++
			} else {
				counts[""]++
			}
		}
		writeSection(&b, title, counts)
	}

	scalar("category_raw", func(r RawItem) string { return r.CategoryRaw })
	scalar("subtype_raw", func(r RawItem) string { return r.SubtypeRaw })
	scalar("rarity_raw", func(r RawItem) string { return r.RarityRaw })
	scalar("attunement_raw", func(r RawItem) string { return r.AttunementRaw })
	scalar("worn_or_held_raw", func(r RawItem) string { return r.WornOrHeldRaw })
	list("effect_kinds_raw", func(r RawItem) []string { return r.EffectKindsRaw })
	list("creatures_mentioned", func(r RawItem) []string { return r.CreaturesMentioned })
	list("environments_mentioned", func(r RawItem) []string { return r.EnvironmentsMentioned })
	list("spells_mentioned", func(r RawItem) []string { return r.SpellsMentioned })
	list("limitations_raw", func(r RawItem) []string { return r.LimitationsRaw })
	presence("charges_text (presence)", func(r RawItem) string { return r.ChargesText })
	presence("cursed_text (presence)", func(r RawItem) string { return r.CursedText })
	presence("variant_table_text (presence)", func(r RawItem) string { return r.VariantTableText })
	list("oddities", func(r RawItem) []string { return r.Oddities })

	return b.String()
}

func writeSection(b *strings.Builder, title string, counts map[string]int) {
	type kv struct {
		val   string
		count int
	}
	var rows []kv
	for v, c := range counts {
		rows = append(rows, kv{v, c})
	}
	// Sort by count desc, then value, so output is deterministic and the
	// dominant vocabulary reads top-down.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].val < rows[j].val
	})
	fmt.Fprintf(b, "\n## %s (%d distinct)\n\n", title, len(rows))
	for _, r := range rows {
		label := r.val
		if label == "" {
			label = "(empty / not stated)"
		}
		fmt.Fprintf(b, "- %3d × %s\n", r.count, label)
	}
}
