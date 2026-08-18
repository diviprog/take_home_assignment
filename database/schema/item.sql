-- requires: foundation, types, base_item

-- The catalog entry itself. Columns fall into three groups: canonical facets
-- (typed, queryable — the ontology proper), provenance (the source's verbatim
-- words, kept so every normalization stays auditable), and the applicability
-- constraint decomposed from the type-line parenthetical.
create table item (
  name          nonempty_text not null,
  category      item_category not null,
  rarity        rarity,
  is_consumable boolean not null default false,

  -- Attunement. The allowlist of who may attune, when restricted, lives in
  -- item_attunement_allowlist; the (id, requires_attunement) unique pair
  -- below lets that table prove its item really requires attunement.
  requires_attunement boolean not null default false,

  -- Wear/wield slot. NULL = unknown even after inference; provenance says
  -- whether a non-null slot was stated by the source or inferred from the
  -- category. hands_required only applies to wielded items (greatswords
  -- occupy both hands; a shield occupies one).
  worn_slot            worn_slot,
  worn_slot_provenance slot_provenance,
  hands_required       smallint,

  -- Applicability: which base items can carry this enchantment, decomposed
  -- from the type-line parenthetical ("(any sword)", "(plate)").
  applicability            applicability_kind,
  base_item_id             uuid,
  applies_weapon_family    weapon_family,
  applies_min_armor_weight armor_weight,

  -- Curses and drawbacks. is_cursed only when the source says cursed; the
  -- text also holds artifact drawbacks (vulnerabilities, increased appetite)
  -- that are detrimental but not curses — the hazard effect tag makes those
  -- queryable.
  is_cursed              boolean not null default false,
  curse_or_drawback_text text,

  -- Provenance: the source's own words and where in the PDF the item lives.
  type_line       nonempty_text not null,
  worn_phrase_raw text,
  description     text not null,
  pdf_page_start  smallint not null,
  pdf_page_end    smallint not null,

  primary key (id),
  unique (id, requires_attunement),

  -- An item's base item must belong to the item's own category (composite FK
  -- onto base_item's (id, category) pair).
  foreign key (base_item_id, category) references base_item (id, category),

  constraint item_slot_provenance_paired check ((worn_slot is null) = (worn_slot_provenance is null)),
  constraint item_hands_range check (hands_required between 1 and 2),
  constraint item_hands_only_wielded check (hands_required is null or worn_slot in ('main_hand', 'off_hand')),
  constraint item_pages_positive check (pdf_page_start > 0 and pdf_page_end > 0),
  constraint item_pages_ordered check (pdf_page_end >= pdf_page_start),
  -- The applicability kind dictates exactly which constraint columns are set.
  constraint item_applicability_shape check (
    case applicability
      when 'specific' then
        base_item_id is not null and applies_weapon_family is null and applies_min_armor_weight is null
      when 'family_or_class' then
        base_item_id is null and ((applies_weapon_family is null) <> (applies_min_armor_weight is null))
      when 'any_in_category' then
        base_item_id is null and applies_weapon_family is null and applies_min_armor_weight is null
      else -- no parenthetical: no constraint columns at all
        base_item_id is null and applies_weapon_family is null and applies_min_armor_weight is null
    end)
) inherits (object);

comment on table item is 'One enchanted item for sale in Reznar''s catalog.';
comment on column item.name is 'Item title as printed (e.g. ''Amulet of Amplified Emotion'').';
comment on column item.category is 'Canonical top-level kind, normalized from the type line (''Wonderous'' typo folded).';
comment on column item.rarity is 'Canonical rarity grade. NULL when the source says ''varies'' — the per-variant grades then live in item_variant — or when genuinely unstated.';
comment on column item.is_consumable is 'True for single-use items (potions). Kept as data, not derived from category, so a future consumable wondrous item needs no schema change.';
comment on column item.requires_attunement is 'Whether the item consumes one of its user''s attunement slots.';
comment on column item.worn_slot is 'Canonical body/hand slot; the one-object-per-slot rule keys on this.';
comment on column item.worn_slot_provenance is 'stated = the source names where it is worn; inferred = derived from category.';
comment on column item.hands_required is 'For wielded items, how many hands it occupies (a greatsword needs 2).';
comment on column item.applicability is 'Shape of the base-item constraint from the type-line parenthetical; NULL when the type line has none.';
comment on column item.base_item_id is 'The single base item this enchantment applies to, when applicability = specific.';
comment on column item.applies_weapon_family is 'Family quantifier for ''any sword''-style constraints.';
comment on column item.applies_min_armor_weight is 'Minimum armor weight for ''any medium or heavy armor''-style constraints (enum order is light < medium < heavy).';
comment on column item.is_cursed is 'True only when the source explicitly marks the item cursed.';
comment on column item.curse_or_drawback_text is 'Verbatim curse or detrimental-property wording (artifact vulnerabilities, costs of use).';
comment on column item.type_line is 'The italic type line verbatim, misspellings included — provenance for every normalized facet.';
comment on column item.worn_phrase_raw is 'The source''s own wear/wield phrase, when it states one — provenance for worn_slot.';
comment on column item.description is 'Complete body prose, verbatim, stitched across page breaks.';
comment on column item.pdf_page_start is 'First PDF page (1-based) the item appears on in data/items_combined.pdf.';
comment on column item.pdf_page_end is 'Last PDF page the item''s prose runs onto.';

create trigger touch before update on item for each row execute function touch();
create index item_category_idx on item (category);
create index item_rarity_idx on item (rarity);
create index item_worn_slot_idx on item (worn_slot);
