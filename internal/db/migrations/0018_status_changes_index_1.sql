drop index ix_status_changes_model_id;
create index ix_status_changes_model_id_timestamp on status_changes (model_id, timestamp) include (status);
