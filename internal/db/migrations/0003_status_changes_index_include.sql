drop index ix_status_changes_model_id_is_latest;

create unique index ix_status_changes_model_id_is_latest
on status_changes (model_id)
include (status, timestamp)
where is_latest = true;
