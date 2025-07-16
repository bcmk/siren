create table long_status_changes as
with periods as (
    select
        model_id,
        status,
        timestamp,
        is_latest,

        lead(timestamp) over (partition by model_id order by timestamp) as next_timestamp,
        lead(status)    over (partition by model_id order by timestamp) as next_status,
        lead(is_latest) over (partition by model_id order by timestamp) as next_is_latest,

        lag(timestamp)  over (partition by model_id order by timestamp) as prev_timestamp,
        lag(status)     over (partition by model_id order by timestamp) as prev_status,
        lag(is_latest)  over (partition by model_id order by timestamp) as prev_is_latest
    from status_changes
)
select model_id, timestamp, status, is_latest
from periods
where
    is_latest
    or (status = 1 and (next_is_latest or next_timestamp - timestamp >= 600))
    or (status = 2 and (prev_timestamp is null or timestamp - prev_timestamp >= 600))
order by timestamp;

drop index ix_status_changes_model_id_is_latest;
drop index ix_status_changes_status_is_latest;
drop index ix_status_changes_timestamp;
drop index ix_status_changes_model_id;

create index long_status_changes_timestamp_btree on long_status_changes (timestamp);
cluster long_status_changes using long_status_changes_timestamp_btree;
drop index long_status_changes_timestamp_btree;

alter table status_changes rename to status_changes_backup;
alter table long_status_changes rename to status_changes;

create index ix_status_changes_model_id on status_changes (model_id);
create index ix_status_changes_timestamp on status_changes using brin ("timestamp") with (pages_per_range = 8);
create unique index ix_status_changes_model_id_is_latest on status_changes (model_id) include (status, timestamp) where is_latest = true;
create index ix_status_changes_status_is_latest on status_changes (status) include (model_id, timestamp) where is_latest = true;
