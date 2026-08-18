-- requires: foundation, types, item

-- Who may attune a restricted item. 48 of 53 attunement items are
-- unrestricted; the five restricted ones name disjunctive allowlists mixing
-- character classes ("by a bard, cleric, ...") with an ancestry ("by an
-- elf") — hence a growable reference table with a kind discriminator rather
-- than a class enum. (The Exo-Armor's alignment restriction lands here too,
-- as kind = other.)
create table attuner_kind (
  name nonempty_text not null,
  kind attuner_kind_type not null,
  primary key (id),
  unique (name)
) inherits (object);
comment on table attuner_kind is 'A kind of creature that an attunement restriction can name: a class (bard), an ancestry (elf), or another wording (good alignment).';
comment on column attuner_kind.name is 'Canonical lowercase singular name (''bard'', ''elf'').';
comment on column attuner_kind.kind is 'Which taxonomy the name belongs to.';

create trigger touch before update on attuner_kind for each row execute function touch();

-- Junction: rows exist only for RESTRICTED attunement items. Semantics
-- follow the source's "or": any listed kind may attune. No rows + item
-- requires attunement = anyone may attune.
create table item_attunement_allowlist (
  item_id             uuid not null,
  attuner_kind_id     uuid not null references attuner_kind (id) on delete restrict,
  -- Constant-true column whose composite FK proves the referenced item
  -- actually requires attunement — an allowlist on a no-attunement item is
  -- unrepresentable, not just discouraged.
  requires_attunement boolean not null default true,
  primary key (id),
  unique (item_id, attuner_kind_id),
  foreign key (item_id, requires_attunement) references item (id, requires_attunement) on delete cascade,
  constraint allowlist_implies_attunement check (requires_attunement)
) inherits (object);
comment on table item_attunement_allowlist is 'Disjunctive allowlist of who may attune a restricted item; absence of rows means anyone may (when the item requires attunement at all).';
comment on column item_attunement_allowlist.item_id is 'The restricted item.';
comment on column item_attunement_allowlist.attuner_kind_id is 'One kind permitted to attune it.';
comment on column item_attunement_allowlist.requires_attunement is 'Always true; exists so the composite FK can enforce that the item requires attunement.';

create trigger touch before update on item_attunement_allowlist for each row execute function touch();
