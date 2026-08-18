-- requires: foundation, types, item

-- Usage limits, one clause per row: items carry up to five (Amulet of Undead
-- Control), so this is one-to-many by nature. kind classifies the MECHANIC
-- (closed, 13 values); detail keeps the clause verbatim (open, lossless).
-- Ownership rules from the vocabulary freeze:
--   * charge mechanics live here as kind = 'charges' (no separate charges
--     field elsewhere — one owner);
--   * environment conditions ("fully immersed in water", "in a wooded
--     environment") are activation_condition rows — with two genuine
--     observations, environment is a clause detail, not its own dimension;
--   * a clause that merely restates the wear condition ("while wearing this
--     amulet") belongs to worn_slot and is NOT inserted here;
--   * attunement requirements belong to item.requires_attunement, not here.
create table item_limitation (
  item_id uuid not null references item (id) on delete cascade,
  kind    limitation_kind not null,
  detail  nonempty_text not null,
  primary key (id),
  unique (item_id, kind, detail)
) inherits (object);

comment on table item_limitation is 'One usage-limit clause of an item: its mechanical kind plus the source''s verbatim wording. No rows = unlimited use.';
comment on column item_limitation.item_id is 'The limited item.';
comment on column item_limitation.kind is 'Mechanical classification of the clause.';
comment on column item_limitation.detail is 'The clause verbatim — magnitudes and phrasing preserved for audit; numeric decomposition can be added later as nullable columns without remodeling.';

create trigger touch before update on item_limitation for each row execute function touch();
create index item_limitation_kind_idx on item_limitation (kind);
