package extraction

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oddities/database/generated"
	"oddities/internal/discovery"
)

// InsertCounts tallies what one item contributed, for the reconciliation
// report — every child row accounted for, nothing silently dropped.
type InsertCounts struct {
	Variants, Attuners, Effects, Targets, Limitations, Spells int
}

// Insert writes one validated item and all its child rows inside the given
// transaction. The caller owns the transaction so a failure rolls back the
// whole item — a half-inserted item is worse than a quarantined one.
func Insert(ctx context.Context, tx pgx.Tx, src discovery.StitchedItem, n NormalizedItem) (InsertCounts, error) {
	q := generated.New(tx)
	var counts InsertCounts

	// Base item first: the item row's composite FK (base_item_id, category)
	// needs it to exist, with the same category the item claims.
	var baseID pgtype.UUID
	if n.Applicability == string(generated.ApplicabilityKindSpecific) {
		id, err := q.UpsertBaseItem(ctx, generated.UpsertBaseItemParams{
			Name:     strings.ToLower(strings.TrimSpace(n.BaseItemName)),
			Category: generated.ItemCategory(n.Category),
			Family:   enumPtr[generated.WeaponFamily](n.BaseItemWeaponFamily),
			Weight:   enumPtr[generated.ArmorWeight](n.BaseItemArmorWeight),
		})
		if err != nil {
			return counts, fmt.Errorf("base item %q: %w", n.BaseItemName, err)
		}
		baseID = id
	}

	itemID, err := q.InsertItem(ctx, generated.InsertItemParams{
		Name:                  src.Name,
		Category:              generated.ItemCategory(n.Category),
		Rarity:                enumPtr[generated.Rarity](n.Rarity),
		IsConsumable:          n.IsConsumable,
		RequiresAttunement:    n.RequiresAttunement,
		WornSlot:              enumPtr[generated.WornSlot](n.WornSlot),
		WornSlotProvenance:    enumPtr[generated.SlotProvenance](n.SlotProvenance),
		HandsRequired:         smallPtr(n.HandsRequired),
		Applicability:         enumPtr[generated.ApplicabilityKind](n.Applicability),
		BaseItemID:            baseID,
		AppliesWeaponFamily:   enumPtr[generated.WeaponFamily](n.AppliesWeaponFamily),
		AppliesMinArmorWeight: enumPtr[generated.ArmorWeight](n.AppliesMinArmorWeight),
		IsCursed:              n.IsCursed,
		CurseOrDrawbackText:   strPtr(n.CurseOrDrawbackText),
		TypeLine:              src.TypeLine,
		WornPhraseRaw:         strPtr(src.WornOrHeldRaw),
		Description:           src.Description,
		PdfPageStart:          int16(src.PdfPageStart),
		PdfPageEnd:            int16(src.PdfPageEnd),
	})
	if err != nil {
		return counts, fmt.Errorf("item row: %w", err)
	}

	for _, v := range n.Variants {
		if _, err := q.InsertItemVariant(ctx, generated.InsertItemVariantParams{
			ItemID: itemID, VariantKey: v.VariantKey,
			Detail: strPtr(v.Detail), Rarity: enumPtr[generated.Rarity](v.Rarity),
		}); err != nil {
			return counts, fmt.Errorf("variant %q: %w", v.VariantKey, err)
		}
		counts.Variants++
	}

	for _, a := range n.Attuners {
		kindID, err := q.UpsertAttunerKind(ctx, generated.UpsertAttunerKindParams{
			Name: strings.ToLower(strings.TrimSpace(a.Name)),
			Kind: generated.AttunerKindType(a.Kind),
		})
		if err != nil {
			return counts, fmt.Errorf("attuner kind %q: %w", a.Name, err)
		}
		if _, err := q.InsertAttunementAllowlist(ctx, generated.InsertAttunementAllowlistParams{
			ItemID: itemID, AttunerKindID: kindID,
		}); err != nil {
			return counts, fmt.Errorf("allowlist %q: %w", a.Name, err)
		}
		counts.Attuners++
	}

	for _, e := range n.Effects {
		if _, err := q.InsertItemEffect(ctx, generated.InsertItemEffectParams{
			ItemID: itemID, Effect: generated.EffectCategory(e.Category), SourceLabel: strPtr(e.SourceLabel),
		}); err != nil {
			return counts, fmt.Errorf("effect %q: %w", e.Category, err)
		}
		counts.Effects++
	}

	for _, t := range n.CreatureTargets {
		if _, err := q.InsertCreatureTarget(ctx, generated.InsertCreatureTargetParams{
			ItemID:  itemID,
			Family:  generated.CreatureFamily(t.Family),
			Role:    generated.TargetRole(t.Role),
			Species: strPtr(strings.ToLower(t.Species)), MinCr: smallPtr(t.MinCR),
			Qualifier: strPtr(t.Qualifier),
		}); err != nil {
			return counts, fmt.Errorf("creature target %s/%s: %w", t.Family, t.Role, err)
		}
		counts.Targets++
	}

	for _, l := range n.Limitations {
		if _, err := q.InsertItemLimitation(ctx, generated.InsertItemLimitationParams{
			ItemID: itemID, Kind: generated.LimitationKind(l.Kind), Detail: l.Detail,
		}); err != nil {
			return counts, fmt.Errorf("limitation %q: %w", l.Kind, err)
		}
		counts.Limitations++
	}

	for _, s := range n.Spells {
		spellID, err := q.UpsertSpell(ctx, generated.UpsertSpellParams{
			Name: strings.ToLower(strings.TrimSpace(s.Name)), School: enumPtr[generated.SpellSchool](s.School),
		})
		if err != nil {
			return counts, fmt.Errorf("spell %q: %w", s.Name, err)
		}
		if _, err := q.InsertItemSpell(ctx, generated.InsertItemSpellParams{
			ItemID: itemID, SpellID: spellID,
		}); err != nil {
			return counts, fmt.Errorf("item spell %q: %w", s.Name, err)
		}
		counts.Spells++
	}

	return counts, nil
}

// enumPtr turns a validated non-empty string into a typed enum pointer, and
// "" into nil — the *T shape sqlc emits for nullable enum columns.
func enumPtr[T ~string](s string) *T {
	if s == "" {
		return nil
	}
	v := T(s)
	return &v
}

func strPtr(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func smallPtr(v int) *int16 {
	if v == 0 {
		return nil
	}
	s := int16(v)
	return &s
}
