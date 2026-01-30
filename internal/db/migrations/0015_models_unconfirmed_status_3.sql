-- Rename status to confirmed_status in models
alter table models rename column status to confirmed_status;
alter table models rename constraint chk_models_status to chk_models_confirmed_status;

-- Add unconfirmed status columns to models (last two status changes)
alter table models
    add column unconfirmed_status integer not null default 0,
    add column unconfirmed_timestamp integer not null default 0,
    add column prev_unconfirmed_status integer not null default 0,
    add column prev_unconfirmed_timestamp integer not null default 0,
    add constraint chk_models_unconfirmed_status check (unconfirmed_status in (0, 1, 2)),
    add constraint chk_models_prev_unconfirmed_status check (prev_unconfirmed_status in (0, 1, 2));

-- Populate from status_changes using window function
update models m set
    unconfirmed_status = sub.unconfirmed_status,
    unconfirmed_timestamp = sub.unconfirmed_timestamp,
    prev_unconfirmed_status = sub.prev_unconfirmed_status,
    prev_unconfirmed_timestamp = sub.prev_unconfirmed_timestamp
from (
    select
        model_id,
        status as unconfirmed_status,
        timestamp as unconfirmed_timestamp,
        coalesce(lead(status) over w, 0) as prev_unconfirmed_status,
        coalesce(lead(timestamp) over w, 0) as prev_unconfirmed_timestamp,
        row_number() over w as rn
    from status_changes
    window w as (partition by model_id order by timestamp desc)
) sub
where sub.rn = 1 and m.model_id = sub.model_id;

-- Drop temporary index and fix BRIN by clustering
drop index ix_status_changes_model_timestamp;
cluster status_changes using ix_status_changes_timestamp_btree;
drop index ix_status_changes_timestamp_btree;
analyze status_changes;
