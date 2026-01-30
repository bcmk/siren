alter table interactions rename to sent_message_log;
alter index ix_interactions_timestamp rename to ix_sent_message_log_timestamp;
