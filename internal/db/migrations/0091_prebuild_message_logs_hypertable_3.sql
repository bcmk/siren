-- Prebuild step 3: the copies' indexes.

-- After the copy: built once over the chunks, not maintained row by row.

create index ix_sent_message_log_new_timestamp on sent_message_log_new (timestamp);

create index ix_sent_message_log_new_user_id_timestamp on sent_message_log_new (user_id, timestamp);

create index ix_received_message_log_new_timestamp on received_message_log_new (timestamp);

create index ix_received_message_log_new_user_id_timestamp on received_message_log_new (user_id, timestamp);

drop procedure convert_message_logs();
