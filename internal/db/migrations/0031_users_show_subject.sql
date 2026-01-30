alter table users add column show_subject boolean not null default true;
alter table notification_queue add column subject text;
