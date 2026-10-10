-- Cutover step 7, during downtime: append the tails and swap the copies in.

-- Everything below reads the old logs and then drops them,
-- so a bot still serving would have its later logs discarded without a word.
-- Dropping them drops their foreign keys' triggers on users,
-- so users is locked up front too, or a reader of users and a log could deadlock the cutover.
lock table users, sent_message_log, received_message_log in access exclusive mode;

-- A row written after the copy but stamped below the boundary, by a clock an hour behind, is lost
insert into sent_message_log_new (priority, timestamp, endpoint, user_id, result, latency, kind, command, reply_seq)
select priority, timestamp, endpoint, user_id, result, latency, kind, command, reply_seq
from sent_message_log
where timestamp >= (select boundary from message_logs_conversion);

insert into received_message_log_new (timestamp, endpoint, user_id, command)
select timestamp, endpoint, user_id, command
from received_message_log
where timestamp >= (select boundary from message_logs_conversion);

drop table message_logs_conversion;
drop table sent_message_log;
drop table received_message_log;

alter table sent_message_log_new rename to sent_message_log;
alter table received_message_log_new rename to received_message_log;

-- Renaming a table does not rename its indexes
alter index ix_sent_message_log_new_timestamp rename to ix_sent_message_log_timestamp;
alter index ix_sent_message_log_new_user_id_timestamp rename to ix_sent_message_log_user_id_timestamp;
alter index ix_received_message_log_new_timestamp rename to ix_received_message_log_timestamp;
alter index ix_received_message_log_new_user_id_timestamp rename to ix_received_message_log_user_id_timestamp;
