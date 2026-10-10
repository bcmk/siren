# TimescaleDB

`status_changes`, `performance_log`, `sent_message_log` and `received_message_log`
are hypertables partitioned by `timestamp`.
The column holds unix seconds, so the chunk interval is in seconds too: 7 days.

Production runs the Apache-2 edition (`timescaledb.license = apache`).
It has hypertables, chunk exclusion, `drop_chunks`, and `time_bucket`.
Compression (columnstore), continuous aggregates,
and policies are Timescale-License-only and error out there.
Tests and `schema-dump` run on the matching `-oss` image, `pgtest.Image`,
so a feature outside the edition fails in tests first.
Bump the image together with the extension version production installs.

Their `timestamp` indexes are plain btrees,
one copy per chunk (`create_default_indexes => false` keeps TimescaleDB from adding its own).
Unlike a BRIN, a btree does not depend on row order,
so rows can be deleted or compacted freely, and `vacuum` and `cluster` are safe at any time.

Count rows with `approximate_row_count`, not `pg_class.reltuples`: the rows live in the chunks.

A unique index builds neither concurrently nor chunk by chunk,
and a managed instance's role cannot build one chunk by chunk by hand:
`_timescaledb_internal` is not its to create in.
So adding one without downtime means a staged copy, as 0081–0085 did.
The compactor stays stopped from a staged copy's prebuild through its cutover,
as `docs/status-changes.md` says.

## Foreign keys

TimescaleDB copies a hypertable's foreign key onto every chunk,
and each copy adds its own triggers to the referenced table.
`sent_message_log` and `received_message_log` reference `users`, so with a chunk a week:

- Every update of `users` runs a check per chunk, which makes bulk updates several times slower
- The first insert of a week creates a chunk and takes `share row exclusive` on `users`,
  so it waits behind any open writer of `users`
- `drop_chunks` takes `access exclusive` on `users`

We keep them so a delete from `users` fails instead of orphaning log rows.
`status_changes` has none, for the per-row cost (`docs/status-changes.md`).

## Background workers

TimescaleDB runs one launcher plus one scheduler process per database that has the extension.
Each is a PostgreSQL background worker and holds a slot from `max_worker_processes` for good,
as do the logical replication, pg_cron and failover-slots launchers,
and every parallel query worker takes one while it runs.
With the default of 8 slots and twelve bot databases, four schedulers fill the pool:
the rest never start, no job can start, and no query gets a parallel worker.

Two caps grow with the database count.
`timescaledb.max_background_workers` must hold the launcher, one scheduler per database,
and a few spare.
`max_worker_processes` must hold that, the three other launchers,
and the parallel workers wanted, which `max_parallel_workers` caps in turn.
On the Apache edition no job can run, so the schedulers only idle, which is harmless.
Sizing and setting them is the deployment's job; adding a bot database means recomputing them.
