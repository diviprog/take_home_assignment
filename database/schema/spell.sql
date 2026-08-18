-- requires: foundation, types, item

-- Spells the catalog references. Open reference table, emphatically not an
-- enum: the survey found 18 distinct spells with an 83% singleton rate, and
-- the deep-relabel of the artifacts immediately added nine more — every
-- shipment will name spells never seen before. Each spell is classified into
-- its school once, here, so per-item classification cannot drift.
create table spell (
  name   nonempty_text not null,
  school spell_school,
  primary key (id),
  unique (name)
) inherits (object);
comment on table spell is 'A named spell that some catalog item casts, stores, or mimics.';
comment on column spell.name is 'Canonical lowercase spell name. The three ''locate *'' spells are distinct and never folded.';
comment on column spell.school is 'School of magic; null until curated for a newly seen spell.';

create trigger touch before update on spell for each row execute function touch();

create table item_spell (
  item_id  uuid not null references item (id) on delete cascade,
  spell_id uuid not null references spell (id) on delete restrict,
  primary key (id),
  unique (item_id, spell_id)
) inherits (object);
comment on table item_spell is 'This item casts, stores, or mimics this spell.';
comment on column item_spell.item_id is 'The referencing item.';
comment on column item_spell.spell_id is 'The referenced spell.';

create trigger touch before update on item_spell for each row execute function touch();
