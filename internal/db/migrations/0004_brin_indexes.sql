drop index ix_interactions_endpoint;
drop index ix_interactions_timestamp;
drop index ix_status_changes_timestamp;

create index ix_interactions_timestamp on interactions using brin ("timestamp");
create index ix_status_changes_timestamp on status_changes using brin ("timestamp");
