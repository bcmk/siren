alter table notification_queue add column viewers integer;
alter table notification_queue add column show_kind integer not null default 0;
