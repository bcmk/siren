-- Prebuild step 1: the copy as a hypertable, with a btree in place of the BRIN index,
-- filled up to a boundary while the bot keeps writing to the old table.
-- A chunk a week takes only that week's rows, so a new row can't land in an old week's pages,
-- as it does in a single table (docs/brin-maintenance.md).

-- The constraints carry their final names: a rename of the table would leave them behind
create table performance_log_new (
    timestamp integer constraint performance_log_timestamp_not_null not null,
    kind integer constraint performance_log_kind_not_null not null,
    duration_ms integer constraint performance_log_duration_ms_not_null not null,
    data jsonb constraint performance_log_data_not_null not null
) with (
    tsdb.hypertable,
    tsdb.partition_column = 'timestamp',
    tsdb.chunk_interval = 604800, -- 7 days
    tsdb.create_default_indexes = false,
    tsdb.columnstore = false
);

-- The boundary stays an hour behind now, as a log row can be stamped before it is written
create table performance_log_conversion (
    boundary integer not null
);

insert into performance_log_conversion (boundary)
values (extract(epoch from now())::integer - 3600);

insert into performance_log_new (timestamp, kind, duration_ms, data)
select timestamp, kind, duration_ms, data
from performance_log
where timestamp < (select boundary from performance_log_conversion);

create index ix_performance_log_new_timestamp on performance_log_new (timestamp);
