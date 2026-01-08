create table long_status_changes as
with periods as (
    select
        channel_id,
        status,
        timestamp,

        lead(timestamp) over (partition by channel_id order by timestamp) as next_timestamp,
        lead(status)    over (partition by channel_id order by timestamp) as next_status,

        lag(timestamp)  over (partition by channel_id order by timestamp) as prev_timestamp,
        lag(status)     over (partition by channel_id order by timestamp) as prev_status
    from status_changes
)
select channel_id, timestamp, status
from periods
where
    next_timestamp is null
    or (status = 1 and (next_timestamp is null or next_timestamp - timestamp >= 600))
    or (status = 2 and (prev_timestamp is null or timestamp - prev_timestamp >= 600))
order by timestamp;

create index long_status_changes_timestamp_btree on long_status_changes (timestamp);
cluster long_status_changes using long_status_changes_timestamp_btree;
drop index long_status_changes_timestamp_btree;

alter table status_changes rename to status_changes_backup;
alter table long_status_changes rename to status_changes;

create index ix_status_changes_channel_id_timestamp on status_changes (channel_id, timestamp) include (status);
create index ix_status_changes_timestamp on status_changes using brin (timestamp) with (pages_per_range = 8);
analyze status_changes;
