alter table streamers add column poll boolean not null default false;
alter table streamers add column poll_error_count integer not null default 0;
create index ix_streamers_poll on streamers (id) include (nickname) where poll;
