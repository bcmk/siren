drop index ix_sent_message_log_timestamp;

create index ix_sent_message_log_timestamp_btree on sent_message_log ("timestamp");
cluster sent_message_log using ix_sent_message_log_timestamp_btree;
drop index ix_sent_message_log_timestamp_btree;

create index ix_sent_message_log_timestamp on sent_message_log using brin ("timestamp") with (pages_per_range = 8);


drop index ix_received_message_log_timestamp;

create index ix_received_message_log_timestamp_btree on received_message_log ("timestamp");
cluster received_message_log using ix_received_message_log_timestamp_btree;
drop index ix_received_message_log_timestamp_btree;

create index ix_received_message_log_timestamp on received_message_log using brin ("timestamp") with (pages_per_range = 8);
