# TimescaleDB

`status_changes` is a hypertable partitioned by `timestamp`.
The column holds unix seconds, so the chunk interval is in seconds too: 7 days.

Production runs the Apache-2 edition (`timescaledb.license = apache`).
It has hypertables, chunk exclusion, `drop_chunks`, and `time_bucket`.
Compression (columnstore), continuous aggregates,
and policies are Timescale-License-only and error out there.
Tests and `schema-dump` run on the matching `-oss` image, `pgtest.Image`,
so a feature outside the edition fails in tests first.
Bump the image together with the extension version production installs.

`ix_status_changes_timestamp` is a plain btree, one copy per chunk
(`create_default_indexes => false` keeps TimescaleDB from adding its own).
Unlike the BRIN it replaced, it does not depend on row order,
so rows can be deleted or compacted freely, and `vacuum` and `cluster` are safe at any time.

The conversion is a staged copy on the prev_status pipeline's pattern:
prebuild migrations stream closed weeks into the hypertable while the bot serves,
and the cutover checks the copy, appends the tail, and swaps the table in.
It needs free disk for a second copy of the table.
Rows a rename fold deletes during the prebuild survive in the copy as orphaned history.
`pg_class.reltuples` of the table no longer counts its rows; `approximate_row_count` does.
