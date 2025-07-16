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
select *
from periods
where status = next_status;
