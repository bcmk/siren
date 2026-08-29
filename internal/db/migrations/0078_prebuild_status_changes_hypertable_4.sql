-- Prebuild step 4: the copy's indexes and constraints.

-- After the copy: built once over the chunks, not maintained row by row.
-- The tail 0080 appends is small enough to maintain them.

alter table status_changes_new add constraint chk_status_changes_status check (status in (0, 1, 2));

alter table status_changes_new
add constraint chk_status_changes_prev_status check (prev_status in (0, 1, 2));

alter table status_changes_new
add constraint chk_status_changes_prev_status_differs check (status = 0 or status <> prev_status);

create index ix_status_changes_new_streamer_id_timestamp
on status_changes_new (streamer_id, timestamp)
include (status, prev_status);

-- A btree, not BRIN as before: it does not depend on row order, so history can be compacted freely
create index ix_status_changes_new_timestamp
on status_changes_new (timestamp);

drop procedure convert_status_changes();
