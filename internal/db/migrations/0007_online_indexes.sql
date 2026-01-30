drop index ix_status_changes_status_is_latest;

create index ix_status_changes_model_id_is_online
on status_changes (model_id)
include (status, timestamp)
where status = 2 and is_latest;

create index ix_models_model_id_is_online
on models (model_id)
where status = 2;
