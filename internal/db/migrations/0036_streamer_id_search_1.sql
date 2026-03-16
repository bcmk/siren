create extension if not exists pg_trgm;

delete from streamers where streamer_id = '';

alter table streamers
add constraint chk_streamers_streamer_id_not_empty
check (streamer_id != '');

create index ix_streamers_streamer_id_trgm_gin
on streamers using gin (streamer_id gin_trgm_ops);

create function max_repeated_alnum_run(text)
returns int as $fn$
    select coalesce(max(length(m[1])), 0)
    from regexp_matches($1, '(([a-z0-9])\2*)', 'g') as m
$fn$ language sql immutable;

create function max_nonalnum_run(text)
returns int as $fn$
    select coalesce(max(length(m[1])), 0)
    from regexp_matches($1, '([^a-z0-9]+)', 'g') as m
$fn$ language sql immutable;

create index ix_streamers_streamer_id_max_repeated_alnum_run
on streamers (max_repeated_alnum_run(streamer_id))
where max_repeated_alnum_run(streamer_id) >= 5;

create index ix_streamers_streamer_id_max_nonalnum_run
on streamers (max_nonalnum_run(streamer_id))
include (streamer_id)
where max_nonalnum_run(streamer_id) >= 3;

create index ix_streamers_streamer_id_prefix
on streamers using btree (streamer_id text_pattern_ops);
