-- Cutover step 6, during downtime: check the copy, append the tail, swap the table in.

-- The prebuild migrations copied every week below the boundary.

-- Everything below reads status_changes and then drops it,
-- so a bot still serving would have its later rounds discarded without a word.
lock table status_changes in access exclusive mode;

-- The bot stamps rows with the current time and the copy stops a margin behind it,
-- so only a clock stepping back past the margin could land a row below the boundary
-- after its week was copied, and the swap would drop it.
-- Deletes since the copy leave the source smaller, never bigger, and pass.
do $$
declare
    below bigint;
    copied bigint;
begin
    select count(*) into below
    from status_changes
    where timestamp < (select boundary from status_changes_conversion);

    select copied_rows into copied from status_changes_conversion;

    if below > copied then
        raise exception 'the source holds % rows below the boundary but only % were copied', below, copied;
    end if;
end $$;

insert into status_changes_new (timestamp, streamer_id, status, prev_status)
select timestamp, streamer_id, status, prev_status
from status_changes
where timestamp >= (select boundary from status_changes_conversion);

drop table status_changes_conversion;
drop table status_changes;

alter table status_changes_new rename to status_changes;

-- Renaming a table renames neither its indexes nor its constraints,
-- and postgres named the not-null ones after the table it created them on.
alter table status_changes
rename constraint status_changes_new_timestamp_not_null to status_changes_timestamp_not_null;

alter table status_changes
rename constraint status_changes_new_streamer_id_not_null to status_changes_streamer_id_not_null;

alter table status_changes
rename constraint status_changes_new_status_not_null to status_changes_status_not_null;

alter table status_changes
rename constraint status_changes_new_prev_status_not_null to status_changes_prev_status_not_null;

alter index ix_status_changes_new_streamer_id_timestamp
rename to ix_status_changes_streamer_id_timestamp;

alter index ix_status_changes_new_timestamp rename to ix_status_changes_timestamp;
