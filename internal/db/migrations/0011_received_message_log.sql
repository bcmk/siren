create table received_message_log (
    timestamp integer not null,
    endpoint text not null,
    chat_id bigint not null,
    command text
);

create index ix_received_message_log_timestamp on received_message_log using brin ("timestamp");
