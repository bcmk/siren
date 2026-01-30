alter table feedback add column timestamp integer;
update feedback set timestamp = extract(epoch from now())::integer;
alter table feedback alter column timestamp set not null;
