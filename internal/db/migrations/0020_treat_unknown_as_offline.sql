-- Treat unknown confirmed status as offline
update models set confirmed_status = 1 where confirmed_status = 0;
alter table models alter column confirmed_status set default 1;
alter table models drop constraint chk_models_confirmed_status;
alter table models add constraint chk_models_confirmed_status check (confirmed_status in (1, 2));
