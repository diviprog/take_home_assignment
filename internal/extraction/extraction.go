// Package extraction is Pass B: the strict half of the two-pass pipeline.
// Pass A (internal/discovery) surveyed the catalog in the source's own words;
// the vocabulary freeze (notes/discovery/freeze.md) turned that survey into
// the closed enums and ownership rules now living in database/schema. This
// package re-extracts every item AGAINST that frozen vocabulary — a model
// call whose output shape only admits frozen members — then validates the
// result mechanically, inserts it through the sqlc-generated queries, and
// quarantines anything that does not fit instead of bending it in.
//
// The division of labor is deliberate: the model does the semantic work
// (which slot is a "circlet", is "3 charges" a charges clause), the Go code
// does the trust work (is every returned value actually in the frozen set,
// does the shape satisfy the schema's constraints before the database sees
// it). Nothing reaches Postgres on the model's word alone.
package extraction

import (
	"fmt"
	"strings"

	"oddities/database/generated"
	"oddities/internal/discovery"
)

// NormalizedItem is one catalog item translated into the frozen vocabulary —
// field-for-field the shape of the item row plus its child rows. Closed
// fields are strings here (the model's JSON) and are checked against the
// frozen sets by Validate before anything is typed for insertion.
type NormalizedItem struct {
	Category     string `json:"category"`
	Rarity       string `json:"rarity"` // "" = NULL: rarity varies (variants carry grades) or is unstated
	IsConsumable bool   `json:"is_consumable"`

	RequiresAttunement bool      `json:"requires_attunement"`
	Attuners           []Attuner `json:"attuners"` // empty = anyone may attune (when required at all)

	WornSlot       string `json:"worn_slot"`       // "" = unknown even after inference
	SlotProvenance string `json:"slot_provenance"` // required iff worn_slot set
	HandsRequired  int    `json:"hands_required"`  // 0 = NULL; only for wielded slots

	Applicability         string `json:"applicability"` // "" = type line has no parenthetical
	BaseItemName          string `json:"base_item_name"`
	BaseItemWeaponFamily  string `json:"base_item_weapon_family"`
	BaseItemArmorWeight   string `json:"base_item_armor_weight"`
	AppliesWeaponFamily   string `json:"applies_weapon_family"`
	AppliesMinArmorWeight string `json:"applies_min_armor_weight"`

	IsCursed            bool   `json:"is_cursed"`
	CurseOrDrawbackText string `json:"curse_or_drawback_text"`

	Variants        []Variant        `json:"variants"`
	Effects         []Effect         `json:"effects"`
	CreatureTargets []CreatureTarget `json:"creature_targets"`
	Limitations     []Limitation     `json:"limitations"`
	Spells          []SpellRef       `json:"spells"`

	// Unplaced collects anything the model judged mechanically meaningful but
	// could not express in the vocabulary above. Non-empty Unplaced does not
	// block insertion; it is surfaced in the reconciliation report so a human
	// decides whether the vocabulary needs to grow.
	Unplaced []string `json:"unplaced"`
}

type Attuner struct {
	Name string `json:"name"` // canonical lowercase singular ("bard", "elf")
	Kind string `json:"kind"` // attuner_kind_type
}

type Variant struct {
	VariantKey string `json:"variant_key"` // source-verbatim ("GP", "Violet")
	Detail     string `json:"detail"`
	Rarity     string `json:"rarity"` // "" when rarity does not vary
}

type Effect struct {
	Category    string `json:"category"`
	SourceLabel string `json:"source_label"` // the survey label(s) this tag folds
}

type CreatureTarget struct {
	Family    string `json:"family"`
	Role      string `json:"role"`
	Species   string `json:"species"` // lowercase; "" when family is the whole statement
	MinCR     int    `json:"min_cr"`  // 0 = no threshold
	Qualifier string `json:"qualifier"`
}

type Limitation struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"` // verbatim clause
}

type SpellRef struct {
	Name   string `json:"name"`   // canonical lowercase
	School string `json:"school"` // "" = not classifiable from the text
}

// The frozen vocabulary as membership sets, built from the sqlc-generated
// constants so this file cannot drift from the schema: if an enum member is
// added in types.sql and sqlc regenerates, the sets follow.
var (
	categories     = set(generated.ItemCategoryWondrousItem, generated.ItemCategoryWeapon, generated.ItemCategoryArmor, generated.ItemCategoryRing, generated.ItemCategoryPotion)
	rarities       = set(generated.RarityCommon, generated.RarityUncommon, generated.RarityRare, generated.RarityVeryRare, generated.RarityLegendary, generated.RarityArtifact)
	wornSlots      = set(generated.WornSlotHead, generated.WornSlotNeck, generated.WornSlotBack, generated.WornSlotTorso, generated.WornSlotHands, generated.WornSlotFinger, generated.WornSlotFeet, generated.WornSlotMainHand, generated.WornSlotOffHand, generated.WornSlotNone)
	provenances    = set(generated.SlotProvenanceStated, generated.SlotProvenanceInferred)
	applicability  = set(generated.ApplicabilityKindSpecific, generated.ApplicabilityKindFamilyOrClass, generated.ApplicabilityKindAnyInCategory)
	weaponFamilies = set(generated.WeaponFamilySword, generated.WeaponFamilyAxe, generated.WeaponFamilyHammer, generated.WeaponFamilyPick, generated.WeaponFamilyShield)
	armorWeights   = set(generated.ArmorWeightLight, generated.ArmorWeightMedium, generated.ArmorWeightHeavy)
	attunerKinds   = set(generated.AttunerKindTypeClass, generated.AttunerKindTypeAncestry, generated.AttunerKindTypeOther)
	effects        = set(generated.EffectCategoryAttackBonus, generated.EffectCategoryDamageDealing, generated.EffectCategoryDefenseAc, generated.EffectCategoryDamageMitigation, generated.EffectCategorySavesAndChecks, generated.EffectCategoryAbilityScore, generated.EffectCategoryHealing, generated.EffectCategoryMovement, generated.EffectCategoryControlDebuff, generated.EffectCategoryStealthConcealment, generated.EffectCategorySpellcasting, generated.EffectCategorySummoningConjuration, generated.EffectCategorySensesDetection, generated.EffectCategoryHazard, generated.EffectCategoryUtilityMisc)
	limitations    = set(generated.LimitationKindUsesPerPeriod, generated.LimitationKindPerRest, generated.LimitationKindCooldown, generated.LimitationKindCharges, generated.LimitationKindDuration, generated.LimitationKindDepletion, generated.LimitationKindExclusivity, generated.LimitationKindActivationCondition, generated.LimitationKindProximity, generated.LimitationKindResistible, generated.LimitationKindTargetRestriction, generated.LimitationKindTermination, generated.LimitationKindOneTime)
	targetRoles    = set(generated.TargetRoleHarms, generated.TargetRoleControls, generated.TargetRoleSummons, generated.TargetRoleProtectsFrom, generated.TargetRoleOther)
	families       = set(generated.CreatureFamilyBeast, generated.CreatureFamilyConstruct, generated.CreatureFamilyDragon, generated.CreatureFamilyElemental, generated.CreatureFamilyFey, generated.CreatureFamilyFiend, generated.CreatureFamilyHumanoid, generated.CreatureFamilyMonstrosity, generated.CreatureFamilyUndead)
	spellSchools   = set(generated.SpellSchoolAbjuration, generated.SpellSchoolConjuration, generated.SpellSchoolDivination, generated.SpellSchoolEnchantment, generated.SpellSchoolEvocation, generated.SpellSchoolIllusion, generated.SpellSchoolNecromancy, generated.SpellSchoolTransmutation)
)

func set[T ~string](vals ...T) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[string(v)] = true
	}
	return m
}

// Dedupe canonicalizes the model's child lists to the schema's uniqueness
// grain. The model legitimately reports one effect family several times with
// different source labels (a canteen's water-making and its self-cleaning are
// both utility_misc); the ontology says that is ONE tag, so the labels merge
// rather than the item bouncing off unique(item_id, effect). Same treatment
// for the other child tables' unique keys.
func (n *NormalizedItem) Dedupe() {
	effIdx := map[string]int{}
	var eff []Effect
	for _, e := range n.Effects {
		if i, ok := effIdx[e.Category]; ok {
			if e.SourceLabel != "" && !strings.Contains(eff[i].SourceLabel, e.SourceLabel) {
				if eff[i].SourceLabel != "" {
					eff[i].SourceLabel += " | "
				}
				eff[i].SourceLabel += e.SourceLabel
			}
			continue
		}
		effIdx[e.Category] = len(eff)
		eff = append(eff, e)
	}
	n.Effects = eff

	seenLim := map[string]bool{}
	var lims []Limitation
	for _, l := range n.Limitations {
		k := l.Kind + "\x00" + l.Detail
		if !seenLim[k] {
			seenLim[k] = true
			lims = append(lims, l)
		}
	}
	n.Limitations = lims

	seenTgt := map[string]bool{}
	var tgts []CreatureTarget
	for _, t := range n.CreatureTargets {
		k := t.Family + "\x00" + t.Role + "\x00" + strings.ToLower(t.Species)
		if !seenTgt[k] {
			seenTgt[k] = true
			tgts = append(tgts, t)
		}
	}
	n.CreatureTargets = tgts

	seenSpell := map[string]bool{}
	var spells []SpellRef
	for _, s := range n.Spells {
		k := strings.ToLower(strings.TrimSpace(s.Name))
		if !seenSpell[k] {
			seenSpell[k] = true
			spells = append(spells, s)
		}
	}
	n.Spells = spells

	seenAtt := map[string]bool{}
	var atts []Attuner
	for _, a := range n.Attuners {
		k := strings.ToLower(strings.TrimSpace(a.Name))
		if !seenAtt[k] {
			seenAtt[k] = true
			atts = append(atts, a)
		}
	}
	n.Attuners = atts

	seenVar := map[string]bool{}
	var vars []Variant
	for _, v := range n.Variants {
		if !seenVar[v.VariantKey] {
			seenVar[v.VariantKey] = true
			vars = append(vars, v)
		}
	}
	n.Variants = vars
}

// Validate checks one normalized item against the frozen vocabulary and the
// schema's shape constraints BEFORE the database sees it, so a violation
// quarantines the item with a readable reason instead of surfacing as a
// constraint error mid-transaction. The checks mirror item.sql's CHECK
// constraints on purpose; the database remains the final authority.
func Validate(src discovery.StitchedItem, n NormalizedItem) []string {
	var errs []string
	bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	member := func(field, val string, allowed map[string]bool, allowEmpty bool) {
		if val == "" {
			if !allowEmpty {
				bad("%s: required but empty", field)
			}
			return
		}
		if !allowed[val] {
			bad("%s: %q is not in the frozen vocabulary", field, val)
		}
	}

	member("category", n.Category, categories, false)
	member("rarity", n.Rarity, rarities, true)
	member("worn_slot", n.WornSlot, wornSlots, true)
	member("slot_provenance", n.SlotProvenance, provenances, true)
	if (n.WornSlot == "") != (n.SlotProvenance == "") {
		bad("worn_slot and slot_provenance must be set together (slot %q, provenance %q)", n.WornSlot, n.SlotProvenance)
	}
	if n.HandsRequired != 0 {
		if n.HandsRequired < 1 || n.HandsRequired > 2 {
			bad("hands_required: %d outside 1-2", n.HandsRequired)
		}
		if n.WornSlot != string(generated.WornSlotMainHand) && n.WornSlot != string(generated.WornSlotOffHand) {
			bad("hands_required set on non-wielded slot %q", n.WornSlot)
		}
	}

	member("applicability", n.Applicability, applicability, true)
	member("base_item_weapon_family", n.BaseItemWeaponFamily, weaponFamilies, true)
	member("base_item_armor_weight", n.BaseItemArmorWeight, armorWeights, true)
	member("applies_weapon_family", n.AppliesWeaponFamily, weaponFamilies, true)
	member("applies_min_armor_weight", n.AppliesMinArmorWeight, armorWeights, true)
	// Mirror of item_applicability_shape.
	switch n.Applicability {
	case string(generated.ApplicabilityKindSpecific):
		if n.BaseItemName == "" {
			bad("applicability=specific needs base_item_name")
		}
		if n.AppliesWeaponFamily != "" || n.AppliesMinArmorWeight != "" {
			bad("applicability=specific must not set applies_* quantifiers")
		}
	case string(generated.ApplicabilityKindFamilyOrClass):
		if (n.AppliesWeaponFamily == "") == (n.AppliesMinArmorWeight == "") {
			bad("applicability=family_or_class needs exactly one of applies_weapon_family / applies_min_armor_weight")
		}
		if n.BaseItemName != "" {
			bad("applicability=family_or_class must not name a base item")
		}
	default: // any_in_category or no parenthetical
		if n.BaseItemName != "" || n.AppliesWeaponFamily != "" || n.AppliesMinArmorWeight != "" {
			bad("applicability=%q must not set base-item constraint columns", n.Applicability)
		}
	}

	if len(n.Attuners) > 0 && !n.RequiresAttunement {
		bad("attuner allowlist on an item that does not require attunement")
	}
	for _, a := range n.Attuners {
		member("attuner.kind", a.Kind, attunerKinds, false)
		if strings.TrimSpace(a.Name) == "" {
			bad("attuner with empty name")
		}
	}
	for _, v := range n.Variants {
		if strings.TrimSpace(v.VariantKey) == "" {
			bad("variant with empty key")
		}
		member("variant.rarity", v.Rarity, rarities, true)
	}
	for _, e := range n.Effects {
		member("effect.category", e.Category, effects, false)
	}
	for _, t := range n.CreatureTargets {
		member("creature_target.family", t.Family, families, false)
		member("creature_target.role", t.Role, targetRoles, false)
		if t.MinCR < 0 {
			bad("creature_target.min_cr: %d negative", t.MinCR)
		}
	}
	for _, l := range n.Limitations {
		member("limitation.kind", l.Kind, limitations, false)
		if strings.TrimSpace(l.Detail) == "" {
			bad("limitation %q with empty detail (detail is the verbatim clause)", l.Kind)
		}
	}
	for _, s := range n.Spells {
		member("spell.school", s.School, spellSchools, true)
		if strings.TrimSpace(s.Name) == "" {
			bad("spell with empty name")
		}
	}

	// Ratified freeze rules with mechanical teeth.
	if strings.Contains(strings.ToLower(src.TypeLine), "varies") && n.Rarity != "" {
		bad("type line says rarity varies but a flat rarity %q was assigned (freeze: variants carry the grades)", n.Rarity)
	}
	dragonRows := 0
	for _, t := range n.CreatureTargets {
		if t.Family == string(generated.CreatureFamilyDragon) && t.Role == string(generated.TargetRoleControls) {
			dragonRows++
		}
	}
	if dragonRows > 1 {
		bad("%d dragon-control target rows — age brackets are ONE row's qualifier, not separate targets", dragonRows)
	}
	return errs
}
