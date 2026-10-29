-- Cutover step 6, during downtime: settings, counters and referral ids move to chats,
-- and every row naming a user on an endpoint names its chat instead.

-- An old bot still draining must not write while this step reads and rewrites its tables.
-- users goes last, as the writers of the others read it in foreign key checks.
-- A payment or migrate reading users first can still deadlock this step, which a restart retries.
-- A log's writers lock it before its chunks, so the chunks need no lock of their own.
lock table
block, subscriptions, pending_subscriptions, notification_queue, feedback, star_payments, referrals,
only sent_message_log, only received_message_log, users
in access exclusive mode;

-- In this do block, the boundary is a parameter, so only the tails' chunks are read and locked
do $$
declare
    b integer;
begin
    select boundary into b from chats_conversion;

    insert into chats (
        user_id, endpoint, max_subs, reports, show_images, offline_notifications,
        show_subject, silent_messages, member_subscriptions, affiliate_params
    )
    select p.user_id, p.endpoint, u.max_subs, u.reports, u.show_images, u.offline_notifications,
    u.show_subject, u.silent_messages, u.member_subscriptions, u.affiliate_params
    from (
        select user_id, endpoint from sent_message_log where timestamp >= b
        union
        select user_id, endpoint from received_message_log where timestamp >= b
        union
        select user_id, endpoint from subscriptions
        union
        select user_id, endpoint from pending_subscriptions
        union
        select user_id, endpoint from block
        union
        select user_id, endpoint from notification_queue
        union
        select user_id, endpoint from feedback
        union
        select user_id, endpoint from star_payments
    ) p
    join users u on u.id = p.user_id
    where not exists (select 1 from chats c where c.user_id = p.user_id and c.endpoint = p.endpoint);

    -- A row written after the copy but stamped below the boundary,
    -- by a clock an hour behind, is lost
    insert into sent_message_log_new (
        priority, timestamp, chat_id, result, latency, kind, command, reply_seq, image_mode
    )
    select l.priority, l.timestamp, c.id, l.result, l.latency, l.kind, l.command, l.reply_seq, l.image_mode
    from sent_message_log l
    join chats c on c.user_id = l.user_id and c.endpoint = l.endpoint
    where l.timestamp >= b;

    insert into received_message_log_new (timestamp, chat_id, command)
    select l.timestamp, c.id, l.command
    from received_message_log l
    join chats c on c.user_id = l.user_id and c.endpoint = l.endpoint
    where l.timestamp >= b;
end $$;

drop table chats_conversion;

drop procedure convert_chats();

-- Settings and reports changed since the prebuild copied them.
-- Each chat so far takes its user's whole reports count, so no alerted chat reads as never alerted.
-- For simplicity it isn't divided between the chats: a sum over them counts it once per chat.
update chats c set
max_subs = u.max_subs,
reports = u.reports,
show_images = u.show_images,
offline_notifications = u.offline_notifications,
show_subject = u.show_subject,
silent_messages = u.silent_messages,
member_subscriptions = u.member_subscriptions,
affiliate_params = u.affiliate_params
from users u
where u.id = c.user_id
and (
    c.max_subs, c.reports, c.show_images, c.offline_notifications,
    c.show_subject, c.silent_messages, c.member_subscriptions, c.affiliate_params
) is distinct from (
    u.max_subs, u.reports, u.show_images, u.offline_notifications,
    u.show_subject, u.silent_messages, u.member_subscriptions, u.affiliate_params
);

update chats c set block = b.block
from block b
where b.user_id = c.user_id and b.endpoint = c.endpoint and b.block <> 0;

drop table block;

-- Chats so far stay greeted: each has used the bot, so a deep link is never its first contact,
-- except one that only a /direct or a send to the owner tried to reach, greeted for simplicity.
alter table chats alter column greeted set default false;

-- Every user gets a chat on every endpoint some chat is on, one gone from the config included,
-- so dropping the users columns loses no setting.
-- After the settings and reports update, so a new chat starts with no reports.
insert into chats (
    user_id, endpoint, max_subs, show_images, offline_notifications,
    show_subject, silent_messages, member_subscriptions, affiliate_params
)
select u.id, e.endpoint, u.max_subs, u.show_images, u.offline_notifications,
u.show_subject, u.silent_messages, u.member_subscriptions, u.affiliate_params
from users u
cross join (select distinct endpoint from chats) e
where not exists (select 1 from chats c where c.user_id = u.id and c.endpoint = e.endpoint);

-- Every chat takes its user's referral id,
-- so a link shown on any bot still credits the user's chat there.
-- An empty id makes no link.
-- A duplicate, which no index prevented, stays with the lowest user id.
update chats c set referral_id = r.referral_id
from (
    select distinct on (referral_id) user_id, referral_id
    from referrals
    where referral_id <> ''
    order by referral_id, user_id
) r
where r.user_id = c.user_id;

drop table referrals;

-- Checked once over all chats, not per inserted row
alter table chats add constraint fk_chats_user_id
foreign key (user_id) references users(id) on delete cascade;

alter table users
drop column max_subs,
drop column reports,
drop column show_images,
drop column offline_notifications,
drop column show_subject,
drop column silent_messages,
drop column member_subscriptions,
drop column affiliate_params;

-- Set aside rather than dropped, per the Locks section of docs/timescaledb.md
alter table sent_message_log rename to sent_message_log_old;
alter table received_message_log rename to received_message_log_old;
alter table sent_message_log_new rename to sent_message_log;
alter table received_message_log_new rename to received_message_log;

alter table subscriptions add column chat_id integer;

update subscriptions s set chat_id = c.id
from chats c
where c.user_id = s.user_id and c.endpoint = s.endpoint;

alter table subscriptions
drop column user_id,
drop column endpoint,
alter column chat_id set not null,
add constraint subscriptions_pkey primary key (chat_id, streamer_id),
add constraint fk_subscriptions_chat_id foreign key (chat_id) references chats(id) on delete cascade;

alter table pending_subscriptions add column chat_id integer;

update pending_subscriptions s set chat_id = c.id
from chats c
where c.user_id = s.user_id and c.endpoint = s.endpoint;

alter table pending_subscriptions
drop column user_id,
drop column endpoint,
alter column chat_id set not null,
add constraint pending_subscriptions_pkey primary key (chat_id, nickname),
add constraint fk_pending_subscriptions_chat_id foreign key (chat_id) references chats(id) on delete cascade;

alter table notification_queue add column chat_id integer;

update notification_queue n set chat_id = c.id
from chats c
where c.user_id = n.user_id and c.endpoint = n.endpoint;

alter table notification_queue
drop column user_id,
drop column endpoint,
alter column chat_id set not null,
add constraint fk_notification_queue_chat_id foreign key (chat_id) references chats(id) on delete cascade;

create index ix_notification_queue_chat_id on notification_queue (chat_id);

alter table feedback add column chat_id integer;

update feedback f set chat_id = c.id
from chats c
where c.user_id = f.user_id and c.endpoint = f.endpoint;

alter table feedback
drop column user_id,
drop column endpoint,
alter column chat_id set not null,
add constraint fk_feedback_chat_id foreign key (chat_id) references chats(id) on delete restrict;

create index ix_feedback_chat_id on feedback (chat_id);

alter table star_payments add column chat_id integer;

update star_payments p set chat_id = c.id
from chats c
where c.user_id = p.user_id and c.endpoint = p.endpoint;

alter table star_payments
drop column user_id,
drop column endpoint,
alter column chat_id set not null,
add constraint fk_star_payments_chat_id foreign key (chat_id) references chats(id) on delete restrict;

create index ix_star_payments_chat_id on star_payments (chat_id);

-- chat_id now names a row of chats
alter table users rename column chat_id to tg_chat_id;

alter index ix_users_chat_id rename to ix_users_tg_chat_id;

-- chats and subscriptions start on fresh statistics; the other tables rewritten here are small
analyze chats, subscriptions;
