create table pending_subscriptions (
    chat_id bigint not null,
    nickname text not null,
    endpoint text not null,
    checking boolean not null default false,
    referral boolean not null default false,
    primary key (chat_id, nickname, endpoint)
);

insert into pending_subscriptions (chat_id, nickname, endpoint, checking)
select chat_id, nickname, endpoint, confirmed = 2
from subscriptions
where confirmed in (0, 2);

delete from subscriptions where confirmed in (0, 2);

alter table subscriptions drop column confirmed;

insert into streamers (nickname)
select distinct sub.nickname
from subscriptions sub
left join streamers s on s.nickname = sub.nickname
where s.id is null;

alter table subscriptions add column streamer_id integer;

update subscriptions sub
set streamer_id = s.id
from streamers s
where sub.nickname = s.nickname;

alter table subscriptions alter column streamer_id set not null;

alter table subscriptions
add constraint fk_subscriptions_streamer_id
foreign key (streamer_id) references streamers(id);

alter table subscriptions drop constraint subscriptions_pkey;
alter table subscriptions drop column nickname;
alter table subscriptions
add constraint subscriptions_pkey
primary key (chat_id, streamer_id, endpoint);
