-- StatusUnknown, StatusOffline, StatusOnline
alter table models add constraint chk_models_status check (status in (0, 1, 2));
alter table status_changes add constraint chk_status_changes_status check (status in (0, 1, 2));
