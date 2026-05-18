drop index ix_sent_message_log_chat_id;
drop index ix_received_message_log_chat_id;

create index ix_sent_message_log_chat_id_timestamp
on sent_message_log (chat_id, timestamp);

create index ix_received_message_log_chat_id_timestamp
on received_message_log (chat_id, timestamp);
