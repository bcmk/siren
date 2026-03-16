alter table streamers drop constraint streamers_pkey;
alter table streamers add column id integer generated always as identity primary key;
drop index ix_streamers_nickname_prefix;
alter table streamers alter column nickname set data type text collate "C";
create unique index ix_streamers_nickname on streamers (nickname) include (id);

drop index ix_streamers_nickname_status_mismatch;

create index ix_streamers_status_mismatch
on streamers (id)
include (nickname, unconfirmed_status, unconfirmed_timestamp, confirmed_status)
where confirmed_status != unconfirmed_status;

alter table status_changes add column streamer_id integer;

update status_changes sc
set streamer_id = s.id
from streamers s
where sc.nickname = s.nickname;

alter table status_changes alter column streamer_id set not null;

-- Also drops ix_status_changes_nickname_timestamp
alter table status_changes drop column nickname;

create index ix_status_changes_streamer_id_timestamp
on status_changes (streamer_id, timestamp)
include (status);
