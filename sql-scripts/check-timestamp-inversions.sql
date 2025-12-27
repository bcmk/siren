with ordered as (
    select
        ctid,
        timestamp,
        lag(ctid) over (order by ctid) as prev_ctid,
        lag(timestamp) over (order by ctid) as prev_ts
    from status_changes
    where timestamp > extract(epoch from now() - interval '7 days')
)
select
    prev_ctid,
    prev_ts,
    to_timestamp(prev_ts) as prev_time,
    ctid,
    timestamp,
    to_timestamp(timestamp) as curr_time,
    round(prev_ts - timestamp) as gap_seconds
from ordered
where timestamp < prev_ts
order by prev_ts - timestamp desc
limit 20;
