-- requires: foundation, types, item

-- The creatures an item's mechanics reference, with the RELATIONSHIP made
-- explicit: a bane weapon harms fiends, the Horn of Bronze Dragon Control
-- controls one dragon, a figurine summons a wolf, the Feathered Crown
-- protects from air-plane beings. Without role, those opposite purchases
-- would all read as "targeted to X". The closed family facet answers
-- Reznar's browse queries with an indexed equality; the open species text
-- keeps vampire-vs-undead specificity; qualifier carries what neither
-- column can (CR thresholds go in min_cr; age-scaled resistance tables,
-- plane-of-origin wording, and the like stay verbatim in qualifier).
create table item_creature_target (
  item_id  uuid not null references item (id) on delete cascade,
  family   creature_family not null,
  role     target_role not null,
  species  text,
  min_cr   smallint,
  qualifier text,
  primary key (id),
  unique nulls not distinct (item_id, family, role, species),
  constraint creature_target_min_cr_positive check (min_cr is null or min_cr > 0)
) inherits (object);

comment on table item_creature_target is 'A creature (family, optionally species) that this item harms, controls, summons, or protects from.';
comment on column item_creature_target.item_id is 'The targeting item.';
comment on column item_creature_target.family is 'Closed creature-family facet for pattern queries.';
comment on column item_creature_target.role is 'How the item relates to the creature — the difference between an undead-slaying blade and an undead-commanding amulet.';
comment on column item_creature_target.species is 'Specific creature noun when the source names one (''vampire'', ''bronze dragon''), lowercase; null when the family is the whole statement.';
comment on column item_creature_target.min_cr is 'Challenge-rating threshold when the source states one (''CR of 5 or above'').';
comment on column item_creature_target.qualifier is 'Verbatim scoping the typed columns cannot hold (age-bracket resistance tables, ''native to the Elemental Plane of Air'').';

create trigger touch before update on item_creature_target for each row execute function touch();
create index item_creature_target_family_idx on item_creature_target (family, role);
