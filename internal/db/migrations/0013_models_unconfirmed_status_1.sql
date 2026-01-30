-- Create temporary indexes for efficient migration
create index ix_status_changes_model_timestamp on status_changes (model_id, timestamp desc) include (status);
create index ix_status_changes_timestamp_btree on status_changes (timestamp);
