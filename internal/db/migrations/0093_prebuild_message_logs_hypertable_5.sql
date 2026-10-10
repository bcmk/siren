-- Prebuild step 5: check the copied rows against users.

alter table sent_message_log_new validate constraint fk_sent_message_log_user_id;

alter table received_message_log_new validate constraint fk_received_message_log_user_id;
