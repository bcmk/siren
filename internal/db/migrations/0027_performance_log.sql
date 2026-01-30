create table performance_log (
    timestamp integer not null,
    kind integer not null,
    duration_ms integer not null,
    data jsonb not null
);

create index ix_performance_log_timestamp on performance_log using brin ("timestamp") with (pages_per_range = 8);
