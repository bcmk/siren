-- Converts status_changes for migration 0073, leaving it only the rows appended since.
-- The boundary is a ctid, immutable per row,
-- so the two halves stay complementary however long the gap between them,
-- and a clock that steps backwards cannot move it.
-- It holds only while nothing frees space below it before 0073 drops the table.
-- Deletes and updates are the obvious way, but an aborted write leaves dead tuples too,
-- so autovacuum is off here and 0073 checks the two halves account for every row.

-- Reset with the table, which 0073 drops once the conversion is swapped in.
alter table status_changes set (autovacuum_enabled = false);

create table status_changes_new (
    timestamp integer not null,
    streamer_id integer not null,
    status smallint not null,
    prev_status smallint not null
);

-- next_block is how far the conversion has got, so an interrupted run resumes there.
-- converted_rows is what it wrote, which spares 0073 a count over the whole table.
create table status_changes_conversion (
    boundary tid not null,
    next_block integer not null,
    converted_rows bigint not null
);

insert into status_changes_conversion (boundary, next_block, converted_rows)
select coalesce(max(ctid), '(0,0)'::tid), 0, 0 from status_changes;

-- The status each streamer ends the converted rows on,
-- so a chunk's first row for it chains off the chunk before
-- rather than starting again from unknown.
create table status_changes_boundary (
    streamer_id integer primary key,
    status smallint not null,
    timestamp integer not null
);

-- A single statement over the whole table would hold the xmin horizon for its duration,
-- and the bot's dead tuples would pile up unreclaimable behind it.
-- Chunks commit one at a time, so the horizon is pinned per chunk,
-- and the pause between them leaves the bot room to work.
create procedure convert_status_changes() language plpgsql as $$
declare
    chunk_blocks constant integer := 1024;
    -- Only a prebuild pauses: at startup the wait would be pure downtime.
    chunk_pause constant double precision :=
        coalesce(nullif(current_setting('siren.chunk_pause', true), '')::double precision, 0);
    boundary tid;
    from_block integer;
    converted_to integer;
    chunk_start tid;
    chunk_end tid;
    chunk_first integer;
    chunk_last integer;
    last_block integer;
    chunks integer := 0;
    rows_done bigint;
    chunk_rows bigint;
begin
    select c.boundary, c.next_block, c.converted_rows into boundary, from_block, rows_done
    from status_changes_conversion c;

    -- The newest converted timestamp, which the boundary table already holds:
    -- chunks run in timestamp order, so no streamer's entry there ever goes back.
    select coalesce(max(timestamp), 0) into converted_to from status_changes_boundary;

    -- A tid prints as (block,offset) and has no accessor for the block alone.
    last_block := split_part(trim(both '()' from boundary::text), ',', 1)::integer;

    loop
        chunk_start := ('(' || from_block || ',0)')::tid;
        exit when chunk_start > boundary;
        chunk_end := ('(' || (from_block + chunk_blocks) || ',0)')::tid;

        select min(timestamp), max(timestamp) into chunk_first, chunk_last
        from status_changes
        where ctid >= chunk_start and ctid < chunk_end and ctid <= boundary;

        -- Chaining a chunk onto the one before is only right while they are in
        -- timestamp order, which a clock stepping backwards would break.
        if chunk_first < converted_to then
            raise exception 'chunk at block % starts at %, below the % already converted',
            from_block, chunk_first, converted_to;
        end if;

        -- Two check rounds can share a second,
        -- and a tie ordered either way would chain a flap onto itself.
        -- ctid breaks it the way the rows were written.
        insert into status_changes_new (timestamp, streamer_id, status, prev_status)
        select c.timestamp, c.streamer_id, c.status,
        coalesce(
            lag(c.status) over (partition by c.streamer_id order by c.timestamp, c.ctid),
            b.status,
            0)
        from status_changes c
        left join status_changes_boundary b on b.streamer_id = c.streamer_id
        where c.ctid >= chunk_start and c.ctid < chunk_end and c.ctid <= boundary
        order by c.timestamp;

        get diagnostics chunk_rows = row_count;
        rows_done := rows_done + chunk_rows;

        insert into status_changes_boundary (streamer_id, status, timestamp)
        select distinct on (streamer_id) streamer_id, status, timestamp
        from status_changes
        where ctid >= chunk_start and ctid < chunk_end and ctid <= boundary
        order by streamer_id, timestamp desc, ctid desc
        on conflict (streamer_id) do update
        set status = excluded.status, timestamp = excluded.timestamp;

        chunks := chunks + 1;

        from_block := from_block + chunk_blocks;
        converted_to := greatest(converted_to, coalesce(chunk_last, converted_to));

        update status_changes_conversion
        set next_block = from_block, converted_rows = rows_done;
        commit;

        if chunks % 10 = 0 then
            raise notice 'converted % rows, block % of %', rows_done, from_block, last_block;
        end if;

        -- An empty chunk did no work, and a fresh database is nothing but empty chunks.
        if chunk_pause > 0 and chunk_first is not null then
            perform pg_sleep(chunk_pause);
        end if;
    end loop;

    raise notice 'converted % rows in % chunks', rows_done, chunks;
end $$;
