-- Prebuild step 1: the copies as hypertables, their bookkeeping, and the copy procedure.

-- A chunk a week lets old rows go by dropping whole chunks.
-- It needs free disk for a second copy of both logs and their indexes.

-- The constraints carry their final names: a rename of the table would leave them behind
create table sent_message_log_new (
    priority integer constraint sent_message_log_priority_not_null not null,
    timestamp integer constraint sent_message_log_timestamp_not_null not null,
    endpoint text constraint sent_message_log_endpoint_not_null not null,
    user_id bigint constraint sent_message_log_user_id_not_null not null,
    result integer constraint sent_message_log_result_not_null not null,
    latency integer constraint sent_message_log_latency_not_null not null,
    kind integer constraint sent_message_log_kind_not_null not null default 0,
    command text,
    reply_seq integer constraint sent_message_log_reply_seq_not_null not null default 0,
    constraint chk_sent_message_log_priority check (priority in (0, 1))
) with (
    tsdb.hypertable,
    tsdb.partition_column = 'timestamp',
    tsdb.chunk_interval = 604800, -- 7 days
    tsdb.create_default_indexes = false,
    tsdb.columnstore = false
);

create table received_message_log_new (
    timestamp integer constraint received_message_log_timestamp_not_null not null,
    endpoint text constraint received_message_log_endpoint_not_null not null,
    user_id bigint constraint received_message_log_user_id_not_null not null,
    command text
) with (
    tsdb.hypertable,
    tsdb.partition_column = 'timestamp',
    tsdb.chunk_interval = 604800, -- 7 days
    tsdb.create_default_indexes = false,
    tsdb.columnstore = false
);

-- boundary is where the next step copies from, on the hypertables' 7-day grid until the last step
create table message_logs_conversion (
    boundary integer not null
);

-- From 0, which no row precedes: min() would read both logs in full, as BRIN cannot answer it
insert into message_logs_conversion (boundary) values (0);

-- One committed transaction per 10 weeks, about what two weeks of status_changes hold,
-- so the xmin horizon is pinned per step and the pause between steps leaves the bot room to work.
-- The copy stops an hour before now(), a margin for a log row stamped before it is written.
create procedure convert_message_logs() language plpgsql as $$
declare
    -- The pause between steps. Only a prebuild pauses: at startup the wait would be pure downtime.
    chunk_pause constant double precision :=
    coalesce(nullif(current_setting('siren.chunk_pause', true), '')::double precision, 0);
    stop constant integer := extract(epoch from now())::integer - 3600;
    w integer;
    step_end integer;
    rows_done bigint := 0;
    step_count bigint;
    step_rows bigint;
    steps integer := 0;
begin
    select boundary into w from message_logs_conversion;

    while w < stop loop
        step_end := least(w + 6048000, stop); -- 10 weeks, on the chunks' grid

        insert into sent_message_log_new (
            priority, timestamp, endpoint, user_id, result, latency, kind, command, reply_seq
        )
        select priority, timestamp, endpoint, user_id, result, latency, kind, command, reply_seq
        from sent_message_log
        where timestamp >= w and timestamp < step_end;

        get diagnostics step_count = row_count;
        step_rows := step_count;

        insert into received_message_log_new (timestamp, endpoint, user_id, command)
        select timestamp, endpoint, user_id, command
        from received_message_log
        where timestamp >= w and timestamp < step_end;

        get diagnostics step_count = row_count;
        step_rows := step_rows + step_count;
        rows_done := rows_done + step_rows;
        w := step_end;

        update message_logs_conversion set boundary = w;
        commit;

        -- An empty step reads only the BRIN indexes, so it is not counted and needs no pause
        if step_rows > 0 then
            steps := steps + 1;
            if steps % 10 = 0 then
                raise notice 'copied % rows, up to %', rows_done, w;
            end if;

            if chunk_pause > 0 then
                perform pg_sleep(chunk_pause);
            end if;
        end if;
    end loop;

    raise notice 'copied % rows in % steps', rows_done, steps;
end $$;
