-- requires: foundation, types

-- The mundane objects enchantments apply to: dagger, plate, shield, ... An
-- open reference table (INSERT, not migration, when Reznar stocks his first
-- trident) carrying the curated facets that "any sword" / "any medium or
-- heavy armor" constraints quantify over.
create table base_item (
  name         nonempty_text not null,
  category     item_category not null,
  family       weapon_family,
  weight       armor_weight,
  primary key (id),
  unique (name),
  -- (id, category) is redundant with the pk but gives item a composite FK
  -- target, so an item's category provably matches its base item's.
  unique (id, category),
  constraint base_item_family_is_weaponish check (family is null or category in ('weapon', 'armor')),
  constraint base_item_weight_is_armor     check (weight is null or category = 'armor')
) inherits (object);
comment on table base_item is 'A mundane base item type (longsword, half plate, shield) that enchanted items instantiate or constrain over.';
comment on column base_item.name is 'Canonical lowercase name, e.g. ''longsword'', ''half-plate''.';
comment on column base_item.category is 'Which item_category this base item belongs to (weapon or armor in practice).';
comment on column base_item.family is 'Weapon family for "any <family>" constraints; null when the base item is in no family (e.g. dagger, which the source treats as not-a-sword).';
comment on column base_item.weight is 'Armor weight class; null for non-armor.';

create trigger touch before update on base_item for each row execute function touch();
