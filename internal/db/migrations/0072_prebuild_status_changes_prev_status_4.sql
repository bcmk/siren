-- Prebuild step 4: the converted table's indexes and constraints.

-- Kept out of the conversion's transactions: these run once, after the chunks.

alter table status_changes_new add constraint chk_status_changes_status check (status in (0, 1, 2));
alter table status_changes_new add constraint chk_status_changes_prev_status check (prev_status in (0, 1, 2));

-- A change never lands on the status it came from,
-- so prev_status equal to status marks a same-second flap the heap handed over out of order.
-- status 0 is exempt: a streamer's first row defaults prev_status to 0,
-- and its status may be 0 too.
alter table status_changes_new
add constraint chk_status_changes_prev_status_differs check (status = 0 or status <> prev_status);

create index ix_status_changes_new_streamer_id_timestamp
on status_changes_new (streamer_id, timestamp)
include (status, prev_status);

create index ix_status_changes_new_timestamp
on status_changes_new using brin (timestamp) with (pages_per_range = 8);

drop procedure convert_status_changes();
