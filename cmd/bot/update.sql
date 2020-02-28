alter table models add column status integer not null default 0;
update models set status = (select status from statuses where model_id = models.model_id);
