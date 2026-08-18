-- requires: foundation, types, item

-- What the item does, as tags: one row per effect family the item's
-- mechanics touch. Compound effects are the norm ("attack roll and AC
-- bonuses" is two rows), which is why this is a tag table and not a column —
-- a single-valued effect column would silently misfile every hybrid item.
-- Reznar's coarse offensive/defensive dimensions are derivable views over
-- these tags (offense = attack_bonus | damage_dealing; defense = defense_ac
-- | damage_mitigation | saves_and_checks | healing).
create table item_effect (
  item_id      uuid not null references item (id) on delete cascade,
  effect       effect_category not null,
  source_label text,
  primary key (id),
  unique (item_id, effect)
) inherits (object);

comment on table item_effect is 'Tag: this item''s mechanics include this effect family.';
comment on column item_effect.item_id is 'The tagged item.';
comment on column item_effect.effect is 'The effect family.';
comment on column item_effect.source_label is 'The survey''s free-form label(s) this tag was folded from — provenance for the classification.';

create trigger touch before update on item_effect for each row execute function touch();
create index item_effect_effect_idx on item_effect (effect);
