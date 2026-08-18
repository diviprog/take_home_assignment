# Ontology decisions

I chose the schema after the open-vocabulary pass produced 80 stitched items.
The evidence is preserved in `notes/discovery/items.json` and the frequency
tally in `notes/discovery/tally.md`. For each dimension below, the observation
states what appeared in that survey and the implementation states how that
evidence became schema.

## Category

**Observation.** All 80 records use five supplier categories: wondrous item,
weapon, armor, ring, and potion. One record spells “wondrous” as “wonderous.”
The source labels its Universal Scroll as a wondrous item rather than creating
a scroll category.

**Implementation.** `item.category` uses the five-member `item_category` enum.
The spelling error normalizes to `wondrous_item`, while `item.type_line` keeps
the original wording. I did not add speculative genre categories.

## Rarity

**Observation.** The source uses a stable ordered ladder. No current item is
common, but the brief explicitly defines common as its floor. The Pouch of
False Coins says “rarity varies” and assigns a different grade to each coin.

**Implementation.** `rarity` is an ordered PostgreSQL enum from `common` to
`artifact`, allowing comparisons such as “rare or better.” A varying item has
a null parent rarity and typed `item_variant` rows for its concrete grades.

## Variants

**Observation.** Some entries contain keyed tables whose facts differ by coin
type or gemstone color. Flattening these tables would discard the mapping.

**Implementation.** `item_variant` stores a source key, detail, and optional
rarity. It is a child entity because one catalog item can have many variants.

## Worn and wielded slots

**Observation.** The business rule concerns physical conflicts, although the
catalog uses names such as hat, helm, crown, mask, and headband. Many records
do not state a slot, and wielded objects may require one or two hands.

**Implementation.** `worn_slot` represents physical locations, folding those
headwear terms into `head`. `slot_provenance` distinguishes stated values from
inference, and `hands_required` handles wielded-item conflicts. Raw wear text
is retained for review.

## Base-item applicability

**Observation.** Type-line parentheticals mix specific objects (`dagger`,
`plate`) with quantified constraints (`any sword`, `any medium or heavy armor`,
`any`). New inventory can introduce new mundane objects without changing the
meaning of weapon family or armor weight.

**Implementation.** Growable names live in `base_item`. `item.applicability`
records whether a constraint is specific, family/class-based, or any in the
category, with typed weapon-family and armor-weight facets. A composite foreign
key guarantees that a specific base item matches the enchanted item category.

## Attunement

**Observation.** Most of the 53 attunement items are unrestricted. Five name
allowlists such as a class or ancestry, and one uses another kind of condition.

**Implementation.** `item.requires_attunement` stores the common fact.
Restricted users are open `attuner_kind` rows classified as class, ancestry,
or other and connected through `item_attunement_allowlist`. A composite foreign
key makes an allowlist on a non-attunement item unrepresentable.

## Effects

**Observation.** Compound items commonly improve several things at once, and
the survey produced many narrow labels. A single effect column would force an
arbitrary primary classification. Some detrimental mechanics are hazards but
are not explicitly curses.

**Implementation.** `item_effect` is a many-valued tag relation over 15 coarse
effect families. `source_label` preserves the narrower observation that led to
each tag. `hazard` covers harmful mechanics without falsely marking them cursed.

## Creature targets

**Observation.** A creature mention is incomplete without its relationship:
items may harm, control, summon, or protect from the same family. Species and
qualifiers vary freely. The Horn of Bronze Dragon Control’s age rows are one
dragon’s modifier table, not four targets.

**Implementation.** `item_creature_target` combines a closed creature family
and relationship role with open species, CR threshold, and qualifier fields.
The Horn becomes one bronze-dragon control row with the age table in its
qualifier.

## Environments

**Observation.** Only two mechanics genuinely require an operating environment.
Other place names describe origins or destinations, so treating every place as
an environment target would create false relationships.

**Implementation.** Genuine environment requirements are
`item_limitation(kind = 'activation_condition')` clauses. I did not introduce
an environment entity from insufficient and semantically mixed evidence.

## Limitations

**Observation.** Items can have several independent usage rules: charges,
cooldowns, durations, saving throws, target restrictions, and termination
conditions. Their wording and quantities are long-tailed.

**Implementation.** `item_limitation` stores one typed mechanical kind plus the
verbatim clause. This supports category queries without prematurely parsing
every quantity. Wear, attunement, and creature identity have one canonical
owner elsewhere and are not duplicated as limitations.

## Spells

**Observation.** Spell names are long-tailed and the multi-page correction
immediately surfaced additional names. The eight schools of magic form the
stable taxonomy. Similarly named `locate` spells are distinct records.

**Implementation.** `spell` is an open reference entity with an optional
closed `spell_school`; `item_spell` is the many-to-many relationship. Adding a
new spell is data insertion rather than an enum migration.

## Curses and drawbacks

**Observation.** Some detrimental properties are explicitly curses, while
artifact drawbacks and rupture consequences are harmful without being called
curses by the source.

**Implementation.** `item.is_cursed` is true only when the source says so.
`curse_or_drawback_text` preserves the wording, and applicable mechanics also
receive the `hazard` effect tag.

## Provenance and failure handling

**Observation.** Normalization requires judgment, and structured model output
can be syntactically valid while still being semantically wrong.

**Implementation.** Each item keeps its complete description, original type
line, raw wear phrase, and PDF page range. Model output is constrained to the
chosen vocabulary, checked again in Go, and finally enforced by PostgreSQL.
Each item inserts transactionally; invalid records are quarantined with reasons
rather than coerced or partially loaded.

## Deliberate boundary

The ontology structures the dimensions Reznar asked to browse. It does not
decompose every number and unusual conditional in the prose. Those facts remain
in the source description, and the extraction report lists mechanics with no
dedicated field so future schema growth is explicit rather than silent.
