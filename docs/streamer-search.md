## Problem

Telegram clients interpret `__text__` as formatting,
stripping underscores before the message reaches the bot.
Users can't type streamer names containing `__`
in Telegram commands.

## Solution

Telegram Mini App (Web App) opened via inline keyboard button.
User taps "Or Find and Add", searches in the Mini App,
taps a result, and the Mini App calls the add API directly.

## Search implementation

See [siren-fuzzy-search](https://github.com/bcmk/siren-fuzzy-search)
for the multi-leg approach used in the code.

## Why a separate nicknames table

The GIN trigram index is the largest and most expensive index
to maintain during writes.
Every upsert into `streamers` pays the GIN maintenance cost —
even when the nickname column doesn't change —
because PostgreSQL may create new index entries for the new tuple version.
The upsert path (`insert ... on conflict do update`)
is the most expensive, roughly doubling write latency.

The `nicknames` table holds just the `nickname` column and the search indexes
(GIN trigram, `max_repeated_alnum_run`, `max_nonalnum_run`).
It receives only plain inserts of genuinely new nicknames —
no upserts, no updates.
This means GIN maintenance runs
only when a new streamer appears for the first time,
not on every status update cycle.

The `streamers` table keeps its B-tree unique index on `nickname` (collate "C"),
which serves exact match and prefix search legs.
No B-tree is needed on `nicknames`
because those legs continue to query `streamers`.

After upserting into `streamers`,
the application uses `xmax = 0` in the `returning` clause
to identify newly inserted rows and inserts only those into `nicknames`.
