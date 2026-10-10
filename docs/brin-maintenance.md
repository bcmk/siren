# BRIN Index Maintenance

None of our tables has a BRIN index now; this doc applies if one is added.

A BRIN index on `timestamp`, say `ix_dummy_log_timestamp` on `dummy_log`,
relies on physical row order matching timestamp order.
Each range of 8 pages stores the minimum and maximum timestamp in it,
and a query skips only the ranges whose span misses its bounds.

Create the index with `(pages_per_range = 8, autosummarize = on)`:
ranges filled since the last summary stay out of the index, and every query reads them.
`autosummarize` summarizes each range as it fills, without a vacuum.

## Not for rows of varying size

Never use BRIN for a table whose rows vary in size:
inserts put its new rows into old pages,
and each makes queries for recent data read a full BRIN range (8 pages in our case).

New rows go into old pages when some space there is marked as free.
A full page usually still has a bit of empty space at the end,
since its size just doesn't align with the rows' sizes.
Rows differ in size when they hold, e.g., strings or JSON,
so even if a row couldn't fit there at the time, a newer, smaller one can.

Inserts alone are enough to break the order.
For that, this empty space must be marked as free.
What marks it:

- Inserts do: an insert marks a page's free space when it fills the page
- `cluster` doesn't
- Vacuums do again: a vacuum marks every page's free space

After a `cluster`, new rows go to the table's end until a vacuum marks the old pages again.

## Restoring order

`cluster` rewrites a table in an index's order, in O(n) time whatever the existing order.
A BRIN index cannot order it, so it takes a temporary btree:

    create index ix_dummy_log_timestamp_btree on dummy_log (timestamp);
    cluster dummy_log using ix_dummy_log_timestamp_btree;
    drop index ix_dummy_log_timestamp_btree;
    analyze dummy_log;

Follow it with `analyze` alone, never `vacuum`,
which would mark every page's free space and send the next rows back across the table.
Autovacuum runs that vacuum by itself sooner or later, so the order lasts only until then.

`cluster` locks the table out while it runs.
It needs the `maintain` privilege,
which a managed database's privilege tooling can take away even from the owner:
`grant maintain on <table> to current_user` gives it back.

## Detecting it

In order, a week's rows fill a run of pages at the table's end:

    select count(*), count(distinct (ctid::text::point)[0]), min((ctid::text::point)[0])
    from dummy_log
    where timestamp > extract(epoch from now() - interval '7 days')::bigint;

Spread over thousands of pages, or starting far from the end, they are scattered.
`explain (analyze, buffers)` of a query for that week shows the cost:
`Heap Blocks: lossy` far above the pages the week fills.

To list inversions between neighbouring rows,
run `sql-scripts/check-timestamp-inversions.sql` with the table as a psql variable:
`psql -v table=dummy_log -f <script>`.
