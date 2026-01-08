with periods as (
    select
        model_id,
        status,
        timestamp,

        lead(timestamp) over (partition by model_id order by timestamp) as next_timestamp,
        lead(status)    over (partition by model_id order by timestamp) as next_status,

        lag(timestamp)  over (partition by model_id order by timestamp) as prev_timestamp,
        lag(status)     over (partition by model_id order by timestamp) as prev_status
    from status_changes
)
select *
from periods
where status = next_status;
