-- Caps at 5 s how long the serving bot's inserts queue behind the alter's wait for its lock.
set local lock_timeout = '5s';

-- Marks a send that carried a file's bytes, whatever its result.
alter table sent_message_log add column file_upload boolean not null default false;
