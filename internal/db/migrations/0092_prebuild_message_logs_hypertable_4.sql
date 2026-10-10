-- Prebuild step 4: the foreign keys, checking only new rows for now.

-- Adding one locks users against writes until the commit,
-- so this step stays short
-- and step 5 checks the copied rows under a lock that lets users be written.

alter table sent_message_log_new add constraint fk_sent_message_log_user_id
foreign key (user_id) references users(id) on delete restrict not valid;

alter table received_message_log_new add constraint fk_received_message_log_user_id
foreign key (user_id) references users(id) on delete restrict not valid;
