-- requires: foundation, types, item

-- Some items exist in keyed variants: the Pouch of False Coins comes keyed to
-- CP/SP/GP/PP with a DIFFERENT rarity per key (its type line says rarity
-- ''varies''), and the Amulet of Amplified Emotion varies by gemstone color.
-- Modeling variants as child rows keeps those per-variant facts queryable
-- instead of collapsing them into a flag: the pouch's parent row has rarity
-- NULL and four children carrying uncommon..legendary.
create table item_variant (
  item_id     uuid not null references item (id) on delete cascade,
  variant_key nonempty_text not null,
  detail      text,
  rarity      rarity,
  primary key (id),
  unique (item_id, variant_key)
) inherits (object);

comment on table item_variant is 'One keyed variant of an item (a coin type, a gemstone color), with any facet that varies per key.';
comment on column item_variant.item_id is 'The parent catalog item.';
comment on column item_variant.variant_key is 'The value of the varying dimension, source-verbatim (''GP'', ''Violet'').';
comment on column item_variant.detail is 'What this variant does or means, verbatim (''Passionate'' for the violet gemstone).';
comment on column item_variant.rarity is 'This variant''s rarity when the source grades variants separately; null when rarity does not vary.';

create trigger touch before update on item_variant for each row execute function touch();
