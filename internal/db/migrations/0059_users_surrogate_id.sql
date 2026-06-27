-- Give users a stable surrogate id with no backfill.
-- Existing rows copy id from chat_id, so each child table converts
-- by renaming chat_id to user_id (values already match, primary keys follow).
-- New rows get an auto-incremented id from a sequence seeded above every id.
-- chat_id becomes a mutable unique field.
-- migrated_to links a chat dropped by a group-to-supergroup merge
-- to the chat it became.
--
-- Operational tables then get a cascading foreign key to users(id);
-- their orphaned rows (a stray block counter, say) are deleted first.
-- The history tables are left for a following migration instead:
-- their rows stranded by the v3.7.0 chat migration
-- (which moved the operational tables but left history at the old chat_id)
-- are kept here, not deleted:
-- the BRIN-indexed received_message_log and sent_message_log
-- must never have rows deleted.
-- A following commit adds 0060, which resurrects a tombstone user
-- for each stranded id and adds the history foreign keys.

-- alter users table
alter table users add column id bigint;
alter table users drop constraint users_pkey;
-- Populate id from chat_id, and collapse chat_type '' to null
-- so unset has one encoding.
update users set id = chat_id, chat_type = nullif(chat_type, '');
alter table users alter column id set not null;
alter table users add column migrated_to bigint;
alter table users add primary key (id);
create unique index ix_users_chat_id on users (chat_id);

alter table users add constraint fk_users_migrated_to
foreign key (migrated_to) references users(id) on delete set null;

create sequence users_id_seq owned by users.id;

-- alter operational tables
alter table subscriptions rename column chat_id to user_id;
alter table pending_subscriptions rename column chat_id to user_id;
alter table block rename column chat_id to user_id;
alter table notification_queue rename column chat_id to user_id;
alter table referrals rename column chat_id to user_id;
alter table feedback rename column chat_id to user_id;
alter table star_payments rename column chat_id to user_id;
alter table received_message_log rename column chat_id to user_id;
alter table sent_message_log rename column chat_id to user_id;
alter table referral_events rename column referrer_chat_id to referrer_user_id;
alter table referral_events rename column follower_chat_id to follower_user_id;

alter index ix_star_payments_chat_id rename to ix_star_payments_user_id;
alter index ix_received_message_log_chat_id_timestamp rename to ix_received_message_log_user_id_timestamp;
alter index ix_sent_message_log_chat_id_timestamp rename to ix_sent_message_log_user_id_timestamp;
create index ix_notification_queue_user_id on notification_queue (user_id);
create index ix_feedback_user_id on feedback (user_id);
create index ix_referral_events_referrer_user_id on referral_events (referrer_user_id);
create index ix_referral_events_follower_user_id on referral_events (follower_user_id);
create index ix_users_migrated_to on users (migrated_to) where migrated_to is not null;

-- Some operational rows have no matching user (e.g. a stray block counter):
-- delete them so the cascading FK can be added.
do $$
declare n bigint;
begin
	delete from subscriptions where user_id not in (select id from users);
	get diagnostics n = row_count; raise notice 'subscriptions: % orphan rows deleted', n;
	delete from pending_subscriptions where user_id not in (select id from users);
	get diagnostics n = row_count; raise notice 'pending_subscriptions: % orphan rows deleted', n;
	delete from block where user_id not in (select id from users);
	get diagnostics n = row_count; raise notice 'block: % orphan rows deleted', n;
	delete from notification_queue where user_id not in (select id from users);
	get diagnostics n = row_count; raise notice 'notification_queue: % orphan rows deleted', n;
	delete from referrals where user_id not in (select id from users);
	get diagnostics n = row_count; raise notice 'referrals: % orphan rows deleted', n;
end $$;

alter table subscriptions add constraint fk_subscriptions_user_id
foreign key (user_id) references users(id) on delete cascade;
alter table pending_subscriptions add constraint fk_pending_subscriptions_user_id
foreign key (user_id) references users(id) on delete cascade;
alter table block add constraint fk_block_user_id
foreign key (user_id) references users(id) on delete cascade;
alter table notification_queue add constraint fk_notification_queue_user_id
foreign key (user_id) references users(id) on delete cascade;
alter table referrals add constraint fk_referrals_user_id
foreign key (user_id) references users(id) on delete cascade;

-- History rows stranded by the v3.7.0 migration are left in place here,
-- not deleted (the message logs are BRIN-indexed).
-- A following commit adds 0060, which resurrects a tombstone user
-- for each and adds the history FKs.

-- Seed above every user_id, including the stranded history ids
-- 0060 resurrects, so a nextval before 0060 cannot collide with a tombstone id.
select setval('users_id_seq', 1 + greatest(
(select coalesce(max(id), 0) from users),
(select coalesce(max(user_id), 0) from received_message_log),
(select coalesce(max(user_id), 0) from sent_message_log),
(select coalesce(max(user_id), 0) from feedback),
(select coalesce(max(user_id), 0) from star_payments),
(select coalesce(max(referrer_user_id), 0) from referral_events),
(select coalesce(max(follower_user_id), 0) from referral_events)
), false);
alter table users alter column id set default nextval('users_id_seq');
