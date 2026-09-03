-- Prebuild step 3: cluster the converted rows by timestamp.

-- Chunking by streamer wrote the heap in streamer order,
-- so cluster it by timestamp to give the BRIN a column its ranges can prune on.
-- The new table is not in use yet, so the rewrite never waits on the bot.
create index ix_status_changes_new_timestamp_btree on status_changes_new (timestamp);
cluster status_changes_new using ix_status_changes_new_timestamp_btree;
drop index ix_status_changes_new_timestamp_btree;
