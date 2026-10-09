-- Cutover step 2, during downtime: append the tail and swap the copy in.

-- Everything below reads performance_log and then drops it,
-- so a bot still serving would have its later logs discarded without a word.
lock table performance_log in access exclusive mode;

-- A row written after step 1 but stamped below the boundary, by a clock an hour behind, is lost
insert into performance_log_new (timestamp, kind, duration_ms, data)
select timestamp, kind, duration_ms, data
from performance_log
where timestamp >= (select boundary from performance_log_conversion);

drop table performance_log_conversion;
drop table performance_log;

alter table performance_log_new rename to performance_log;

-- Renaming a table does not rename its indexes
alter index ix_performance_log_new_timestamp rename to ix_performance_log_timestamp;
