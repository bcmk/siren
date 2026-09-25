-- Prebuild step 1: the copy, its bookkeeping, and the copy procedure.

-- Building the unique index on the live table would block inserts, so this copy gets it.
-- It needs free disk for a second copy of the table and its indexes.

-- The constraints carry their final names: a rename of the table would leave them behind
create table status_changes_new (
    timestamp integer constraint status_changes_timestamp_not_null not null,
    streamer_id integer constraint status_changes_streamer_id_not_null not null,
    status smallint constraint status_changes_status_not_null not null,
    prev_status smallint constraint status_changes_prev_status_not_null not null,
    constraint chk_status_changes_status check (status in (0, 1, 2)),
    constraint chk_status_changes_prev_status check (prev_status in (0, 1, 2)),
    constraint chk_status_changes_prev_status_differs check (status = 0 or status <> prev_status)
) with (
    tsdb.hypertable,
    tsdb.partition_column = 'timestamp',
    tsdb.chunk_interval = 604800, -- 7 days
    tsdb.create_default_indexes = false,
    tsdb.columnstore = false
);

-- boundary is where the next step copies from, on the hypertable's 7-day grid until the last step.
create table status_changes_conversion (
    boundary integer not null
);

insert into status_changes_conversion (boundary)
select coalesce(min(timestamp), extract(epoch from now())::integer) / 604800 * 604800
from status_changes;

-- One committed transaction per week,
-- so the xmin horizon is pinned per week and the pause between weeks leaves the bot room to work.
-- The copy stops an hour before now(), a margin for a clock stepping back.
-- 0085 appends the newer rows and checks none landed in the day below the boundary.
create procedure convert_status_changes() language plpgsql as $$
declare
    -- The pause between weeks. Only a prebuild pauses: at startup the wait would be pure downtime.
    chunk_pause constant double precision :=
    coalesce(nullif(current_setting('siren.chunk_pause', true), '')::double precision, 0);
    stop constant integer := extract(epoch from now())::integer - 3600;
    w integer;
    step_end integer;
    rows_done bigint := 0;
    step_count bigint;
    steps integer := 0;
begin
    select boundary into w from status_changes_conversion;

    while w < stop loop
        step_end := least(w + 604800, stop);

        insert into status_changes_new (timestamp, streamer_id, status, prev_status)
        select timestamp, streamer_id, status, prev_status
        from status_changes
        where timestamp >= w and timestamp < step_end;

        get diagnostics step_count = row_count;
        rows_done := rows_done + step_count;
        w := step_end;

        update status_changes_conversion set boundary = w;
        commit;

        steps := steps + 1;
        if steps % 10 = 0 then
            raise notice 'copied % rows, up to %', rows_done, w;
        end if;

        if chunk_pause > 0 then
            perform pg_sleep(chunk_pause);
        end if;
    end loop;

    raise notice 'copied % rows in % steps', rows_done, steps;
end $$;
