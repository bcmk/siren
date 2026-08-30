-- Kept out of the conversion's transactions: these run once, after the chunks.

alter table status_changes_new add constraint chk_status_changes_status check (status in (0, 1, 2));
alter table status_changes_new add constraint chk_status_changes_prev_status check (prev_status in (0, 1, 2));

create index ix_status_changes_new_streamer_id_timestamp
on status_changes_new (streamer_id, timestamp)
include (status, prev_status);

create index ix_status_changes_new_timestamp
on status_changes_new using brin (timestamp) with (pages_per_range = 8);

drop procedure convert_status_changes();
