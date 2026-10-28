-- Prebuild step 3: the copies' indexes and foreign keys.

-- After the copy: built once over the chunks, not maintained row by row.

-- The timestamp indexes take their names once the old logs' indexes holding them are gone
create index ix_sent_message_log_new_timestamp on sent_message_log_new (timestamp);

create index ix_sent_message_log_chat_id_timestamp on sent_message_log_new (chat_id, timestamp);

create index ix_received_message_log_new_timestamp on received_message_log_new (timestamp);

create index ix_received_message_log_chat_id_timestamp on received_message_log_new (chat_id, timestamp);

-- Only the copier writes chats and the copies so far, so checking every copied row blocks no one
alter table sent_message_log_new add constraint fk_sent_message_log_chat_id
foreign key (chat_id) references chats(id) on delete restrict;

alter table received_message_log_new add constraint fk_received_message_log_chat_id
foreign key (chat_id) references chats(id) on delete restrict;
