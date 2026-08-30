-- The prebuild migrations converted everything at or below the boundary.

-- Everything below reads status_changes and then drops it,
-- so a bot still serving would have its later rounds discarded without a word.
-- The lock waits for the rounds in flight and holds off the rest,
-- so the checks and the tail see a settled table.
lock table status_changes in access exclusive mode;

-- The boundary splits by ctid while prev_status is defined by timestamp,
-- so a row written after the prebuild
-- but timestamped before its tail would chain off the wrong predecessor.
-- Nothing can check this earlier: the tail does not exist yet.
do $$
declare
    tail_start integer;
    prebuilt_end integer;
begin
    select min(timestamp) into tail_start
    from status_changes
    where ctid > (select boundary from status_changes_conversion);

    select max(timestamp) into prebuilt_end from status_changes_boundary;

    -- A check round shares one timestamp and is never split, so equal is normal.
    if tail_start < prebuilt_end then
        raise exception 'a row written after the prebuild predates it: % is below %',
        tail_start, prebuilt_end;
    end if;
end $$;

-- Rows that vacuum let land below the boundary belong to neither half and would vanish.
do $$
declare
    converted bigint;
    tail bigint;
    total bigint;
begin
    -- The prebuild counted its own inserts, which spares a scan of the table here.
    select converted_rows into converted from status_changes_conversion;

    select count(*) into tail
    from status_changes
    where ctid > (select boundary from status_changes_conversion);

    select count(*) into total from status_changes;

    if converted + tail <> total then
        raise exception 'converted % and tail % do not account for the % rows', converted, tail, total;
    end if;
end $$;

-- A streamer's first row here takes its previous status from where the prebuild left off.
insert into status_changes_new (timestamp, streamer_id, status, prev_status)
select c.timestamp, c.streamer_id, c.status,
coalesce(
    lag(c.status) over (partition by c.streamer_id order by c.timestamp, c.ctid),
    b.status,
    0)
from status_changes c
left join status_changes_boundary b on b.streamer_id = c.streamer_id
where c.ctid > (select boundary from status_changes_conversion)
order by c.timestamp;

drop table status_changes_boundary;
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
