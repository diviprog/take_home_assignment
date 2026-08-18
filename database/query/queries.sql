-- The write path for cmd/extract (Pass B) plus the browse queries Reznar's
-- brief asks for. sqlc turns each into a typed Go method on *Queries.

-- name: InsertBaseItem :one
insert into base_item (name, category, family, weight)
values ($1, $2, $3, $4)
returning id;

-- Get-or-create for base items: several catalog entries share one base item
-- ("dagger" carries three different enchantments), and facets stick once
-- curated — a later row may fill a missing family/weight but never overwrite.
-- name: UpsertBaseItem :one
insert into base_item (name, category, family, weight)
values ($1, $2, $3, $4)
on conflict (name) do update
  set family = coalesce(base_item.family, excluded.family),
      weight = coalesce(base_item.weight, excluded.weight)
returning id;

-- name: InsertItem :one
insert into item (
  name, category, rarity, is_consumable,
  requires_attunement,
  worn_slot, worn_slot_provenance, hands_required,
  applicability, base_item_id, applies_weapon_family, applies_min_armor_weight,
  is_cursed, curse_or_drawback_text,
  type_line, worn_phrase_raw, description, pdf_page_start, pdf_page_end
) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
returning id;

-- name: InsertItemVariant :one
insert into item_variant (item_id, variant_key, detail, rarity)
values ($1, $2, $3, $4)
returning id;

-- name: UpsertAttunerKind :one
insert into attuner_kind (name, kind)
values ($1, $2)
on conflict (name) do update set kind = excluded.kind
returning id;

-- name: InsertAttunementAllowlist :one
insert into item_attunement_allowlist (item_id, attuner_kind_id)
values ($1, $2)
returning id;

-- name: InsertItemEffect :one
insert into item_effect (item_id, effect, source_label)
values ($1, $2, $3)
returning id;

-- name: InsertCreatureTarget :one
insert into item_creature_target (item_id, family, role, species, min_cr, qualifier)
values ($1, $2, $3, $4, $5, $6)
returning id;

-- name: InsertItemLimitation :one
insert into item_limitation (item_id, kind, detail)
values ($1, $2, $3)
returning id;

-- name: UpsertSpell :one
insert into spell (name, school)
values ($1, $2)
on conflict (name) do update set school = coalesce(spell.school, excluded.school)
returning id;

-- name: InsertItemSpell :one
insert into item_spell (item_id, spell_id)
values ($1, $2)
returning id;

-- Reconciliation: how much of the catalog landed, by category.
-- name: CountItemsByCategory :many
select category, count(*) as items
from item
group by category
order by items desc;

-- Reznar: "guide his customers to things that match their needs" — e.g.
-- everything of a rarity or better that a given class can attune.
-- name: ItemsAttunableBy :many
select i.id, i.name, i.rarity
from item i
where i.rarity >= $1
  and (not i.requires_attunement
       or not exists (select 1 from item_attunement_allowlist a where a.item_id = i.id)
       or exists (select 1
                  from item_attunement_allowlist a
                  join attuner_kind k on k.id = a.attuner_kind_id
                  where a.item_id = i.id and k.name = $2))
order by i.rarity desc, i.name;

-- Reznar: one object per slot — everything competing for a given slot.
-- name: ItemsForSlot :many
select id, name, rarity, requires_attunement
from item
where worn_slot = $1
order by rarity desc nulls last, name;

-- Reznar: pattern-finding over effect families.
-- name: ItemsWithEffect :many
select i.id, i.name, i.category, i.rarity
from item i
join item_effect e on e.item_id = i.id
where e.effect = $1
order by i.name;
