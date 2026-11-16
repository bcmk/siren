## Problem

Telegram clients interpret `__text__` as formatting,
stripping underscores before the message reaches the bot.
Users can't type streamer names containing `__`
in Telegram commands.

## Solution

Telegram Mini App (Web App) opened via inline keyboard button.
User taps "Find and Add", searches in the Mini App,
taps a result, and the Mini App calls the add API directly.
A reply about one streamer carries its nickname in the button's `term`,
so the app opens on its results rather than on an empty box.

## Search implementation

See [siren-fuzzy-search](https://github.com/bcmk/siren-fuzzy-search)
for the multi-leg approach used in the code.

The code collects the legs' nicknames and ranks them on the client side.
We chose between two ways: on the database side in a temporary table, or on the client side.
A temporary table keeps the intermediate data in the database,
so only the final result goes to the client.
Collecting on the client sends up to 100 nicknames per leg, at most 301,
over the wire to return at most 7, but a temporary table costs more.
One created per search gives each statement on it a new `pg_stat_statements` entry.
`pg_stat_statements` holds a limited number of entries,
so these push out the statistics of rarely run statements.
One kept for the connection's life holds back the database's `datfrozenxid`
unless its own session vacuums it.

The legs go to the database as one batch, a single round trip.
Ranking in SQL over the collected nicknames would need a second one,
and the `fuzzystrmatch` extension for `levenshtein`, so the ranking runs on the client.

## Web app logging

A search is named `search` in `received_message_log`,
and the submit button's add is named `web_app_add`,
distinct from `add` so a typed command is not conflated with a tap.
Both paths are whitelist-gated and refused outright when the chat is outside it,
so a refused request appears in neither log.

## Why a separate nicknames table

The GIN trigram index is the largest and most expensive index
to maintain during writes.
A GIN on `streamers` would slow status updates by about 70%.
Each changes columns that `ix_streamers_status_mismatch` covers,
so none is a HOT update, and a GIN would index every new row version.

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

Every path that inserts into `streamers` adds the new streamers to `nicknames`.
