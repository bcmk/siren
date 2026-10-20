-- Caps at 5 s how long the serving bot's queries queue behind the alter's wait for its lock.
set local lock_timeout = '5s';

-- Marks a notification that should show an image its site did not report.
alter table notification_queue add column missing_image boolean not null default false;
