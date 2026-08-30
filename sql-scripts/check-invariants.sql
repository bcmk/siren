-- no status is ever recorded twice in a row
select streamer_id, timestamp, status, prev_status
from status_changes
where status = prev_status;

-- prev_status agrees with the row before it
select streamer_id, timestamp, status, prev_status, preceding_status
from (
    select
        streamer_id,
        timestamp,
        status,
        prev_status,
        lag(status) over (partition by streamer_id order by timestamp, ctid) as preceding_status
    from status_changes
) sc
where prev_status is distinct from coalesce(preceding_status, 0);
