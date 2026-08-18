package extraction

import (
	"strings"
	"testing"

	"oddities/internal/discovery"
)

func okItem() (discovery.StitchedItem, NormalizedItem) {
	src := discovery.StitchedItem{
		RawItem: discovery.RawItem{
			Name:     "Cloak of Testing",
			TypeLine: "Wondrous item, rare (requires attunement)",
		},
		PdfPageStart: 3, PdfPageEnd: 3,
	}
	n := NormalizedItem{
		Category: "wondrous_item", Rarity: "rare",
		RequiresAttunement: true,
		WornSlot:           "back", SlotProvenance: "stated",
		Effects:     []Effect{{Category: "stealth_concealment", SourceLabel: "invisibility"}},
		Limitations: []Limitation{{Kind: "uses_per_period", Detail: "3 times per day"}},
	}
	return src, n
}

func TestValidateAccepts(t *testing.T) {
	src, n := okItem()
	if errs := Validate(src, n); len(errs) != 0 {
		t.Fatalf("valid item rejected: %v", errs)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*NormalizedItem)
		want   string // substring of the expected reason
	}{
		{"off-vocabulary category", func(n *NormalizedItem) { n.Category = "scroll" }, "not in the frozen vocabulary"},
		{"off-vocabulary effect", func(n *NormalizedItem) { n.Effects[0].Category = "damage" }, "not in the frozen vocabulary"},
		{"slot without provenance", func(n *NormalizedItem) { n.SlotProvenance = "" }, "set together"},
		{"hands on unworn item", func(n *NormalizedItem) { n.HandsRequired = 2 }, "non-wielded"},
		{"allowlist without attunement", func(n *NormalizedItem) {
			n.RequiresAttunement = false
			n.Attuners = []Attuner{{Name: "bard", Kind: "class"}}
		}, "does not require attunement"},
		{"specific without base item", func(n *NormalizedItem) { n.Applicability = "specific" }, "needs base_item_name"},
		{"quantifier on specific", func(n *NormalizedItem) {
			n.Applicability = "specific"
			n.BaseItemName = "dagger"
			n.AppliesWeaponFamily = "sword"
		}, "must not set applies_"},
		{"empty limitation detail", func(n *NormalizedItem) { n.Limitations[0].Detail = "  " }, "empty detail"},
		{"flat rarity on varies", func(n *NormalizedItem) {}, "variants carry the grades"}, // src mutated below
		{"split age brackets", func(n *NormalizedItem) {
			n.CreatureTargets = []CreatureTarget{
				{Family: "dragon", Role: "controls", Species: "bronze dragon (wyrmling)"},
				{Family: "dragon", Role: "controls", Species: "bronze dragon (adult)"},
			}
		}, "ONE row"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, n := okItem()
			if tc.name == "flat rarity on varies" {
				src.TypeLine = "Wondrous item, rarity varies"
			}
			tc.mutate(&n)
			errs := Validate(src, n)
			for _, e := range errs {
				if strings.Contains(e, tc.want) {
					return
				}
			}
			t.Fatalf("wanted a reason containing %q, got %v", tc.want, errs)
		})
	}
}
