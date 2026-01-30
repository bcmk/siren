alter index ix_status_changes_timestamp set (pages_per_range = 8);
reindex index ix_status_changes_timestamp;
