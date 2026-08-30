create table long_status_changes as
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

create index long_status_changes_timestamp_btree on long_status_changes (timestamp);
cluster long_status_changes using long_status_changes_timestamp_btree;
drop index long_status_changes_timestamp_btree;

-- Renaming a table leaves its index names behind,
-- so the backup keeps holding the ones recreated below until they are renamed too.
alter table status_changes rename to status_changes_backup;
alter index ix_status_changes_streamer_id_timestamp
rename to ix_status_changes_backup_streamer_id_timestamp;
alter index ix_status_changes_timestamp rename to ix_status_changes_backup_timestamp;

alter table long_status_changes rename to status_changes;

-- create table as select keeps neither, so both come back by hand.
alter table status_changes
alter column timestamp set not null,
alter column streamer_id set not null,
alter column status set not null,
alter column prev_status set not null;

alter table status_changes add constraint chk_status_changes_status check (status in (0, 1, 2));
alter table status_changes add constraint chk_status_changes_prev_status check (prev_status in (0, 1, 2));

create index ix_status_changes_streamer_id_timestamp
on status_changes (streamer_id, timestamp)
include (status, prev_status);
create index ix_status_changes_timestamp on status_changes using brin (timestamp) with (pages_per_range = 8);
analyze status_changes;
