alter table sent_message_log add column command text;
alter table notification_queue add column command text;
alter table pending_subscriptions add column command text;
