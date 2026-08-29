-- Prebuild step 2: the hypertable, the copy's bookkeeping, and the copy procedure.

-- Copies status_changes into a hypertable for migration 0080,
-- leaving it only the rows appended since.
-- The boundary is a timestamp on the hypertable's 7-day grid:
-- every row below it is copied, and the tail above it waits for the cutover.
-- The bot stamps rows with the current time, so only the head of the table gains rows,
-- and a week that ends a margin behind now() is settled.
-- A rename fold may still delete a copied streamer's rows;
-- the copy keeps them, and 0080 tolerates the surplus: orphaned history hurts nothing.

create table status_changes_new (
    timestamp integer not null,
    streamer_id integer not null,
    status smallint not null,
    prev_status smallint not null
) with (
    tsdb.hypertable,
    tsdb.partition_column = 'timestamp',
    tsdb.chunk_interval = 604800, -- 7 days
    tsdb.create_default_indexes = false,
    tsdb.columnstore = false
);

-- boundary is the next week to copy; copied_rows spares 0080 a count over the copy.
-- An empty table starts at the current week, so a fresh database copies nothing.
create table status_changes_conversion (
    boundary integer not null,
    copied_rows bigint not null
);

insert into status_changes_conversion (boundary, copied_rows)
select coalesce(min(timestamp), extract(epoch from now())::integer) / 604800 * 604800, 0
from status_changes;

-- One committed transaction per week, so the xmin horizon is pinned per week
-- and the pause between weeks leaves the bot room to work.
-- A week is [w, w + 604800) on the hypertable's own grid, so each fills exactly one chunk,
-- and the BRIN on the old table serves each week's scan.
-- Weeks newer than the margin wait for the cutover: the bot writes at now(),
-- and the margin absorbs a clock stepping back; 0080 checks nothing slipped under anyway.
create procedure convert_status_changes() language plpgsql as $$
declare
    -- The pause between weeks. Only a prebuild pauses: at startup the wait would be pure downtime.
    chunk_pause constant double precision :=
    coalesce(nullif(current_setting('siren.chunk_pause', true), '')::double precision, 0);
    margin constant integer := 3600;
    w integer;
    rows_done bigint;
    week_count bigint;
    weeks integer := 0;
begin
    select boundary, copied_rows into w, rows_done from status_changes_conversion;

    while w + 604800 <= extract(epoch from now())::integer - margin loop
        insert into status_changes_new (timestamp, streamer_id, status, prev_status)
        select timestamp, streamer_id, status, prev_status
        from status_changes
        where timestamp >= w and timestamp < w + 604800;

        get diagnostics week_count = row_count;
        rows_done := rows_done + week_count;
        w := w + 604800;

        update status_changes_conversion set boundary = w, copied_rows = rows_done;
        commit;

        weeks := weeks + 1;
        if weeks % 10 = 0 then
            raise notice 'copied % rows, weeks up to %', rows_done, w;
        end if;

        if chunk_pause > 0 then
            perform pg_sleep(chunk_pause);
        end if;
    end loop;

    raise notice 'copied % rows in % weeks', rows_done, weeks;
end $$;
