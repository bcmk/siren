-- The clone is a hypertable from the start,
-- so the ordered fill lands each week in its chunk.
create table long_status_changes (
    timestamp integer not null,
    streamer_id integer not null,
    status smallint not null,
    prev_status smallint not null,
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

insert into long_status_changes (timestamp, streamer_id, status, prev_status)
with periods as (
    select
        streamer_id,
        status,
        timestamp,

        lead(timestamp) over (partition by streamer_id order by timestamp) as next_timestamp,
        lead(status)    over (partition by streamer_id order by timestamp) as next_status,

        lag(timestamp)  over (partition by streamer_id order by timestamp) as prev_timestamp,
        lag(status)     over (partition by streamer_id order by timestamp) as prev_status
    from status_changes
),
kept as (
    select streamer_id, timestamp, status
    from periods
    where
        next_timestamp is null
        or (status = 1 and (next_timestamp is null or next_timestamp - timestamp >= 600))
        or (status = 2 and (prev_timestamp is null or timestamp - prev_timestamp >= 600))
),
-- Dropping a row can leave its neighbours on the same status,
-- which would give the second of them a prev_status equal to its own.
-- Only the first of each run survives.
collapsed as (
    select streamer_id, timestamp, status
    from (
        select streamer_id, timestamp, status,
        lag(status) over (partition by streamer_id order by timestamp) as preceding
        from kept
    ) runs
    where preceding is distinct from status
)
-- prev_status is recomputed over what survives, not carried over from the source.
-- A streamer left with only a trailing unknown would take prev_status 0 on a status 0
-- row, so the filter drops it rather than write a row repeating its own status.
select timestamp, streamer_id, status, prev_status
from (
    select timestamp, streamer_id, status,
    coalesce(lag(status) over (partition by streamer_id order by timestamp), 0::smallint) as prev_status
    from collapsed
) recomputed
where status <> prev_status
order by timestamp;

-- Renaming a table leaves its index names behind,
-- so the backup keeps holding the ones recreated below until they are renamed too.
alter table status_changes rename to status_changes_backup;

alter index ix_status_changes_streamer_id_timestamp
rename to ix_status_changes_backup_streamer_id_timestamp;

alter index ix_status_changes_timestamp rename to ix_status_changes_backup_timestamp;

alter table long_status_changes rename to status_changes;

-- Renaming a table leaves the not-null constraint names behind too.
alter table status_changes
rename constraint long_status_changes_timestamp_not_null to status_changes_timestamp_not_null;

alter table status_changes
rename constraint long_status_changes_streamer_id_not_null to status_changes_streamer_id_not_null;

alter table status_changes
rename constraint long_status_changes_status_not_null to status_changes_status_not_null;

alter table status_changes
rename constraint long_status_changes_prev_status_not_null to status_changes_prev_status_not_null;

create index ix_status_changes_streamer_id_timestamp
on status_changes (streamer_id, timestamp)
include (status, prev_status);

create index ix_status_changes_timestamp on status_changes (timestamp);

vacuum analyze status_changes;
