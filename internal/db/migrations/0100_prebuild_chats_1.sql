-- Prebuild step 1: chats, the message logs' copies keyed by chat, and the copy procedure.

-- The copies need free disk for a second copy of both logs and their indexes.
-- chats' foreign key to users waits for the cutover:
-- added here, its checks would block a group's move to a supergroup until each step commits.

-- A feedback row with no user would abort the cutover, so fail here, while the bot still serves
alter table feedback alter column user_id set not null;

-- A chat is a user on one endpoint, holding that bot's settings and counters.
-- Its columns avoid alignment padding: max_subs fills the slot before user_id,
-- and variable-width columns go last.
create table chats (
    id integer generated always as identity primary key,
    max_subs integer not null,
    user_id bigint not null,
    reports integer not null default 0,
    block integer not null default 0,
    show_images boolean not null default true,
    offline_notifications boolean not null default true,
    show_subject boolean not null default true,
    silent_messages boolean not null default false,
    member_subscriptions boolean not null default false,
    endpoint text not null,
    affiliate_params jsonb,
    referral_id text
);

create unique index ix_chats_user_id_endpoint on chats (user_id, endpoint);

create unique index ix_chats_endpoint_referral_id on chats (endpoint, referral_id)
where referral_id is not null;

-- The constraints carry their final names: a rename of the table would leave them behind
create table sent_message_log_new (
    priority integer constraint sent_message_log_priority_not_null not null,
    timestamp integer constraint sent_message_log_timestamp_not_null not null,
    chat_id integer constraint sent_message_log_chat_id_not_null not null,
    result integer constraint sent_message_log_result_not_null not null,
    latency integer constraint sent_message_log_latency_not_null not null,
    kind integer constraint sent_message_log_kind_not_null not null default 0,
    command text,
    reply_seq integer constraint sent_message_log_reply_seq_not_null not null default 0,
    image_mode integer constraint sent_message_log_image_mode_not_null not null default 0,
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
    chat_id integer constraint received_message_log_chat_id_not_null not null,
    command text
) with (
    tsdb.hypertable,
    tsdb.partition_column = 'timestamp',
    tsdb.chunk_interval = 604800, -- 7 days
    tsdb.create_default_indexes = false,
    tsdb.columnstore = false
);

-- boundary is where the next step copies from,
-- on the hypertables' 7-day grid except after a call's last step
create table chats_conversion (
    boundary integer not null
);

-- From 0, which no row precedes: min() would lock every chunk of both logs until this step commits
insert into chats_conversion (boundary) values (0);

-- One committed transaction per 10 weeks,
-- so the xmin horizon is pinned per step and the pause between steps leaves the bot room to work.
-- The copy stops an hour before now(), a margin for a log row stamped before it is written.
create procedure convert_chats() language plpgsql as $$
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
    select boundary into w from chats_conversion;

    while w < stop loop
        -- Up to 10 weeks, ending on the chunks' grid
        step_end := least((w / 604800 + 10) * 604800, stop);

        -- execute plans each step for its own window: a generic plan,
        -- which the sixth execution builds for comparison, would lock every chunk of both logs.
        execute '
            insert into chats (
                user_id, endpoint, max_subs, reports, show_images, offline_notifications,
                show_subject, silent_messages, member_subscriptions, affiliate_params
            )
            select p.user_id, p.endpoint, u.max_subs, u.reports, u.show_images, u.offline_notifications,
            u.show_subject, u.silent_messages, u.member_subscriptions, u.affiliate_params
            from (
                select user_id, endpoint from sent_message_log
                where timestamp >= $1 and timestamp < $2
                union
                select user_id, endpoint from received_message_log
                where timestamp >= $1 and timestamp < $2
            ) p
            join users u on u.id = p.user_id
            where not exists (select 1 from chats c where c.user_id = p.user_id and c.endpoint = p.endpoint)'
        using w, step_end;

        execute '
            insert into sent_message_log_new (
                priority, timestamp, chat_id, result, latency, kind, command, reply_seq, image_mode
            )
            select l.priority, l.timestamp, c.id, l.result, l.latency, l.kind, l.command, l.reply_seq, l.image_mode
            from sent_message_log l
            join chats c on c.user_id = l.user_id and c.endpoint = l.endpoint
            where l.timestamp >= $1 and l.timestamp < $2'
        using w, step_end;

        get diagnostics step_count = row_count;
        step_rows := step_count;

        execute '
            insert into received_message_log_new (timestamp, chat_id, command)
            select l.timestamp, c.id, l.command
            from received_message_log l
            join chats c on c.user_id = l.user_id and c.endpoint = l.endpoint
            where l.timestamp >= $1 and l.timestamp < $2'
        using w, step_end;

        get diagnostics step_count = row_count;
        step_rows := step_rows + step_count;
        rows_done := rows_done + step_rows;
        w := step_end;

        update chats_conversion set boundary = w;
        commit;

        -- An empty step reads no chunk, so it is not counted and needs no pause
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
