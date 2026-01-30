drop index ix_status_changes_model_id_is_latest;
drop index ix_status_changes_model_id_is_online;
alter table status_changes drop column is_latest;
