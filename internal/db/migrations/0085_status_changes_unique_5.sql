-- Cutover step 5, during downtime: check the copy, append the tail, swap the table in.

-- Everything below reads status_changes and then drops it,
-- so a bot still serving would have its later rounds discarded without a word.
lock table status_changes in access exclusive mode;

-- A clock stepping back past the margin could land a row below the boundary,
-- which the swap would drop.
-- Counting the day below the boundary catches a step back shorter than a day.
-- Deletes since the copy only shrink the source, and pass.
do $$
declare
    b integer;
    source_rows bigint;
    copied_rows bigint;
begin
    select boundary into b from status_changes_conversion;

    select count(*) into source_rows
    from status_changes
    where timestamp >= b - 86400 and timestamp < b;

    select count(*) into copied_rows
    from status_changes_new
    where timestamp >= b - 86400 and timestamp < b;

    if source_rows > copied_rows then
        raise exception 'the day below the boundary holds % rows but only % were copied', source_rows, copied_rows;
    end if;

    -- A tie in the tail fails the unique index, and the cutover rolls back with it.
    -- In the block, the boundary is a parameter, so the insert reads only the tail's chunks.
    insert into status_changes_new (timestamp, streamer_id, status, prev_status)
    select timestamp, streamer_id, status, prev_status
    from status_changes
    where timestamp >= b;
end $$;

drop table status_changes_conversion;
drop table status_changes;

alter table status_changes_new rename to status_changes;

-- Renaming a table does not rename its indexes
alter index ix_status_changes_new_streamer_id_timestamp
rename to ix_status_changes_streamer_id_timestamp;

alter index ix_status_changes_new_timestamp rename to ix_status_changes_timestamp;
