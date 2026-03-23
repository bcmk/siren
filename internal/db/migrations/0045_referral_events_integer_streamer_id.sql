insert into streamers (nickname)
select distinct r.nickname
from referral_events r
left join streamers s on s.nickname = r.nickname
where r.nickname is not null
and s.id is null;

alter table referral_events add column streamer_id integer;

update referral_events r
set streamer_id = s.id
from streamers s
where r.nickname = s.nickname;

alter table referral_events
add constraint fk_referral_events_streamer_id
foreign key (streamer_id) references streamers(id);

alter table referral_events drop column nickname;
