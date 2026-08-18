-- requires: foundation

-- The value vocabulary, chosen from the Pass A discovery survey in
-- notes/discovery/items.json and its frequency tally. The reasoning is
-- summarized in notes/decisions.md. Every member below is grounded in
-- observed catalog text unless its comment says otherwise.

create domain nonempty_text as text check (btrim(value) <> '');
comment on domain nonempty_text is 'Text that must contain something other than whitespace.';

-- The five categories this supplier actually uses (80/80 items). Deliberately
-- NOT padded with the wider genre list (rod/staff/wand/scroll): the catalog
-- itself files its one scroll (Universal Scroll) under ''Wondrous item'', so
-- speculative members would be unreachable. A genuinely new type line is an
-- ALTER TYPE ... ADD VALUE migration, which is the correct level of ceremony
-- for a new kind of thing existing.
create type item_category as enum ('wondrous_item', 'weapon', 'armor', 'ring', 'potion');
comment on type item_category is 'Top-level kind from the item''s type line. Closed to the five values the supplier uses.';

-- Ordered: declaration order IS the comparison order, so `rarity >= 'rare'`
-- means "rare or better". ''common'' has zero observed items but is the
-- brief's stated floor ("from common all the way up to artifacts").
-- An item whose printed rarity is ''varies'' (Pouch of False Coins) stores
-- NULL here and carries per-variant grades in item_variant instead.
create type rarity as enum ('common', 'uncommon', 'rare', 'very_rare', 'legendary', 'artifact');
comment on type rarity is 'Rarity ladder, ordered lowest to highest; enum order supports range queries.';

-- One value per body location, per Reznar's rule that only one object can be
-- used per slot. head deliberately merges hat/helmet/mask/crown/headband
-- (his own example). hands (gloves) has zero observed items but is named in
-- the brief. none = explicitly carried/used without occupying a slot (a
-- scroll in your possession, a pipe at your lips — the pipe''s mouth-conflict
-- question is logged in notes/decisions.md as a future ALTER candidate).
create type worn_slot as enum
  ('head', 'neck', 'back', 'torso', 'hands', 'finger', 'feet',
   'main_hand', 'off_hand', 'none');
comment on type worn_slot is 'Canonical wear/wield slot; uniqueness per wearer is the one-object-per-slot rule.';

-- 40% of items never state where they are worn; their slot is inferred from
-- category (weapon -> main_hand, armor -> torso, ...). Recording which is
-- which keeps inferred slots distinguishable from source-stated ones.
create type slot_provenance as enum ('stated', 'inferred');
comment on type slot_provenance is 'Whether worn_slot came from the item''s own text or was inferred from its category.';

-- The subtype parenthetical is really an applicability constraint: 83% of
-- stated values name one base item ("dagger"), the rest quantify over a
-- class ("any sword", "any medium or heavy armor", bare "any").
create type applicability_kind as enum ('specific', 'family_or_class', 'any_in_category');
comment on type applicability_kind is 'Shape of the enchantment''s base-item constraint from the type-line parenthetical.';

create type weapon_family as enum ('sword', 'axe', 'hammer', 'pick', 'shield');
comment on type weapon_family is 'Weapon family used by "any <family>" applicability constraints. Asserts e.g. that a longsword is a sword and a dagger is not — a curated judgment, editable per base_item row.';

-- Ordered so "any medium or heavy armor" is `weight >= 'medium'`.
create type armor_weight as enum ('light', 'medium', 'heavy');
comment on type armor_weight is 'Armor weight class, ordered light to heavy; supports minimum-weight applicability.';

-- The attunement allowlist vocabulary spans two taxonomies (character class
-- and ancestry — "by an elf"), which is why attuner kinds are a growable
-- reference table (attunement.sql) and only this discriminator is closed.
create type attuner_kind_type as enum ('class', 'ancestry', 'other');
comment on type attuner_kind_type is 'What sort of restriction an attuner kind is: a character class, an ancestry, or another wording.';

-- Effect families for the item_effect tag table. Derived from 146 open survey
-- labels plus the deep-relabel of the multi-page artifacts; an item holds one
-- row per family it touches (compound effects are the norm, not the edge
-- case). hazard exists because the catalog contains non-curse drawbacks
-- (Backpack of Holding''s rupture mechanics) that no other axis owns.
create type effect_category as enum
  ('attack_bonus', 'damage_dealing', 'defense_ac', 'damage_mitigation',
   'saves_and_checks', 'ability_score', 'healing', 'movement',
   'control_debuff', 'stealth_concealment', 'spellcasting',
   'summoning_conjuration', 'senses_detection', 'hazard', 'utility_misc');
comment on type effect_category is 'Closed effect families; an item is tagged with every family its mechanics touch.';

-- Limitation kinds classify usage-limit clauses by MECHANIC, not phrasing,
-- which is what keeps the vocabulary closed while the phrases stay open.
-- The verbatim clause always rides along in item_limitation.detail.
create type limitation_kind as enum
  ('uses_per_period', 'per_rest', 'cooldown', 'charges', 'duration',
   'depletion', 'exclusivity', 'activation_condition', 'proximity',
   'resistible', 'target_restriction', 'termination', 'one_time');
comment on type limitation_kind is 'Mechanical kind of a usage-limit clause. Environment conditions (underwater, wooded) are activation_condition rows: with only two genuine environment restrictions observed, environment is a clause detail, not its own dimension.';

-- How an item relates to the creatures it names. Without this, "targeted to
-- undead" conflates a weapon that slays undead, an amulet that commands
-- them, and a figurine that summons a wolf — opposite purchases.
create type target_role as enum ('harms', 'controls', 'summons', 'protects_from', 'other');
comment on type target_role is 'The item''s relationship to a targeted creature: damages it, commands it, conjures it, or defends against it.';

-- All 38 observed creature values land in these nine families; species-level
-- detail stays in open text on the targeting row.
create type creature_family as enum
  ('beast', 'construct', 'dragon', 'elemental', 'fey', 'fiend',
   'humanoid', 'monstrosity', 'undead');
comment on type creature_family is 'Closed creature-family facet for targeting queries; the open species column carries the specific noun.';

-- The eight classical schools of magic — a genuinely complete published
-- taxonomy, safe to close even though conjuration/evocation have no observed
-- support yet. Lives on the spell row, so each spell is classified once.
create type spell_school as enum
  ('abjuration', 'conjuration', 'divination', 'enchantment',
   'evocation', 'illusion', 'necromancy', 'transmutation');
comment on type spell_school is 'School of a referenced spell; enables browse-by-need queries over spellcasting items.';
