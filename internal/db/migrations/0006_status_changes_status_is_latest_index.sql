create index ix_status_changes_status_is_latest
on status_changes (status)
include (model_id, timestamp)
where is_latest = true;
