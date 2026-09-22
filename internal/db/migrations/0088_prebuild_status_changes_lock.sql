-- The table to lock while editing status_changes history, which the compactor does for a run.
-- Its rows are the compactor's coverages: for each rule, the stretch examined at its threshold,
-- stamped with the run's time, which the next run never goes before.
create table status_changes_lock (
    after integer primary key,
    shorter_than integer not null,
    begin_timestamp integer not null,
    end_timestamp integer not null,
    run_timestamp integer not null
);

-- The chunks the compactor's runs finished, each once, vacuumed one a run in the order queued.
-- A rewrite gives a chunk's free space back to the disk, where a vacuum only frees it for reuse.
create table status_changes_vacuum_queue (
    id bigint generated always as identity primary key,
    chunk text not null,
    rewrite boolean not null default false
);

create unique index ix_status_changes_vacuum_queue_chunk on status_changes_vacuum_queue (chunk);
