insert into streamers (nickname)
select distinct n.nickname
from notification_queue n
left join streamers s on s.nickname = n.nickname
where s.id is null;

alter table notification_queue add column streamer_id integer;

update notification_queue n
set streamer_id = s.id
from streamers s
where n.nickname = s.nickname;

alter table notification_queue alter column streamer_id set not null;

alter table notification_queue
add constraint fk_notification_queue_streamer_id
foreign key (streamer_id) references streamers(id);

alter table notification_queue drop column nickname;
