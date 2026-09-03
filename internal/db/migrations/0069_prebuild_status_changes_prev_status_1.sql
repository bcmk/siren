-- Prebuild step 1: the new table, the conversion's bookkeeping, and the conversion procedure.

-- Converts status_changes for migration 0074, leaving it only the rows appended since.
-- The boundary is a ctid, immutable per row,
-- so the two halves stay complementary however long the gap between them,
-- and a clock that steps backwards cannot move it.
-- It holds only while nothing frees space below it before 0074 drops the table.
-- Deletes and updates are the obvious way, but an aborted write leaves dead tuples too,
-- so autovacuum is off here and 0074 checks the two halves account for every row.
-- Databases migrated before v5.5.3 ran an earlier cut of these six steps and skip them all.

create table status_changes_new (
    timestamp integer not null,
    streamer_id integer not null,
    status smallint not null,
    prev_status smallint not null
);

-- next_streamer_id is how far the conversion has got, so an interrupted run resumes there.
-- converted_rows is what it wrote, which spares 0074 a count over the whole table.
-- max_timestamp is the newest converted timestamp, which 0074 checks the tail against.
create table status_changes_conversion (
    boundary tid not null,
    next_streamer_id integer not null,
    converted_rows bigint not null,
    max_timestamp integer not null
);

insert into status_changes_conversion (boundary, next_streamer_id, converted_rows, max_timestamp)
select coalesce(max(ctid), '(0,0)'::tid), 0, 0, 0 from status_changes;

-- Disabled after the boundary scan, not before,
-- so its lock is held for a moment and not across the scan the bot writes through.
-- 0074 drops the table, which resets this.
alter table status_changes set (autovacuum_enabled = false);

-- A single statement over the whole table would hold the xmin horizon for its duration,
-- and the bot's dead tuples would pile up unreclaimable behind it.
-- Chunks commit one at a time, so the horizon is pinned per chunk,
-- and the pause between them leaves the bot room to work.
-- Chunks are cut by streamer over the (streamer_id, timestamp) index the old table already has,
-- so a chunk holds whole streamers whatever order the heap is in,
-- and lag() finds each row's predecessor without looking past the chunk.
create procedure convert_status_changes() language plpgsql as $$
declare
    -- Streamers per chunk.
    -- A chunk holds whole streamers, so its row count follows how many they have;
    -- tests set it low so a handful of streamers still spans several chunks.
    chunk_streamers constant integer :=
    coalesce(nullif(current_setting('siren.chunk_streamers', true), '')::integer, 50000);
    -- Only a prebuild pauses: at startup the wait would be pure downtime.
    chunk_pause constant double precision :=
    coalesce(nullif(current_setting('siren.chunk_pause', true), '')::double precision, 0);
    boundary tid;
    from_id integer;
    max_id integer;
    chunks integer := 0;
    rows_done bigint;
    max_ts integer;
    chunk_count bigint;
    chunk_max integer;
begin
    select c.boundary, c.next_streamer_id, c.converted_rows, c.max_timestamp
    into boundary, from_id, rows_done, max_ts
    from status_changes_conversion c;

    select max(streamer_id) into max_id from status_changes where ctid <= boundary;

    while max_id is not null and from_id <= max_id loop
        -- A streamer's rows are all at or below the boundary or all above it only by chance,
        -- so ctid <= boundary keeps the tail out and 0074 chains it back on.
        with ins as (
            insert into status_changes_new (timestamp, streamer_id, status, prev_status)
            select c.timestamp, c.streamer_id, c.status,
            coalesce(lag(c.status) over w, 0)
            from status_changes c
            where c.streamer_id >= from_id
            and c.streamer_id < from_id + chunk_streamers
            and c.ctid <= boundary
            window w as (partition by c.streamer_id order by c.timestamp, c.ctid)
            returning timestamp
        )
        select count(*), coalesce(max(timestamp), 0) into chunk_count, chunk_max from ins;

        rows_done := rows_done + chunk_count;
        max_ts := greatest(max_ts, chunk_max);
        from_id := from_id + chunk_streamers;

        update status_changes_conversion
        set next_streamer_id = from_id, converted_rows = rows_done, max_timestamp = max_ts;
        commit;

        chunks := chunks + 1;
        if chunks % 10 = 0 then
            raise notice 'converted % rows, streamers up to %', rows_done, from_id;
        end if;

        if chunk_pause > 0 then
            perform pg_sleep(chunk_pause);
        end if;
    end loop;

    raise notice 'converted % rows in % chunks', rows_done, chunks;
end $$;
