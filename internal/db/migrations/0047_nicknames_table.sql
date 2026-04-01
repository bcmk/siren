create table nicknames (
    nickname text collate "C" not null
);

insert into nicknames (nickname)
select nickname from streamers;

drop index ix_streamers_nickname_trgm_gin;
drop index ix_streamers_nickname_max_repeated_alnum_run;
drop index ix_streamers_nickname_max_nonalnum_run;

create index ix_nicknames_nickname_trgm_gin
on nicknames using gin (nickname gin_trgm_ops);

create index ix_nicknames_nickname_max_repeated_alnum_run
on nicknames (max_repeated_alnum_run(nickname))
where max_repeated_alnum_run(nickname) >= 5;

create index ix_nicknames_nickname_max_nonalnum_run
on nicknames (max_nonalnum_run(nickname))
include (nickname)
where max_nonalnum_run(nickname) >= 3;
