alter table sent_message_log add column reply_seq integer not null default 0;
alter table notification_queue add column reply_seq integer not null default 0;
alter table pending_subscriptions add column reply_seq integer not null default 0;
