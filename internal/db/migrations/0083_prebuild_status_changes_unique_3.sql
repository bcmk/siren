-- Prebuild step 3: the copy's indexes.

-- After the copy: built once over the chunks, not maintained row by row.
-- A tie in the copied history fails the unique index here, while the bot still serves.

create unique index ix_status_changes_new_streamer_id_timestamp
on status_changes_new (streamer_id, timestamp)
include (status, prev_status);

create index ix_status_changes_new_timestamp
on status_changes_new (timestamp);

drop procedure convert_status_changes();
