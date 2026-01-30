alter table users
add column created_at bigint not null default extract(epoch from now())::bigint;

create index ix_users_created_at on users (created_at);
