-- Resurrect the history stranded by the v3.7.0 chat migration.
-- 0059 renamed chat_id to user_id but left these rows in place:
-- their user had moved away, and received_message_log and sent_message_log
-- are BRIN-indexed, so we never delete from them.
-- Recreate a tombstone user (id = chat_id = the stranded id) for each,
-- so the history is kept and the foreign keys below validate.
-- The message logs are only read to find stranded ids, never rewritten,
-- so their BRIN clustering is undisturbed.

do $$
declare n bigint;
begin
	insert into users (id, chat_id)
	select sid, sid from (
		select user_id as sid from feedback
		where user_id is not null and user_id not in (select id from users)
		union
		select user_id from star_payments
		where user_id not in (select id from users)
		union
		select user_id from received_message_log
		where user_id not in (select id from users)
		union
		select user_id from sent_message_log
		where user_id not in (select id from users)
		union
		select referrer_user_id from referral_events
		where referrer_user_id is not null and referrer_user_id not in (select id from users)
		union
		select follower_user_id from referral_events
		where follower_user_id is not null and follower_user_id not in (select id from users)
	) s
	-- Target id, not any unique index:
	-- a chat_id-only clash breaks the id = chat_id invariant and must surface.
	on conflict (id) do nothing;
	get diagnostics n = row_count;
	raise notice 'resurrected % tombstone users for stranded history', n;
end $$;

-- No re-seed here: 0059 already seeded users_id_seq above every stranded id,
-- and these explicit-id inserts do not advance the sequence.

-- These are all audit or history tables, and no user-delete path exists,
-- so every foreign key restricts:
-- a stray delete from users must fail loudly, not silently erase records.
-- This matters most for the BRIN message logs, which are never deleted from,
-- and equally for payments, feedback, and referral attribution.
alter table feedback add constraint fk_feedback_user_id
foreign key (user_id) references users(id) on delete restrict;
alter table star_payments add constraint fk_star_payments_user_id
foreign key (user_id) references users(id) on delete restrict;
alter table received_message_log add constraint fk_received_message_log_user_id
foreign key (user_id) references users(id) on delete restrict;
alter table sent_message_log add constraint fk_sent_message_log_user_id
foreign key (user_id) references users(id) on delete restrict;
alter table referral_events add constraint fk_referral_events_referrer_user_id
foreign key (referrer_user_id) references users(id) on delete restrict;
alter table referral_events add constraint fk_referral_events_follower_user_id
foreign key (follower_user_id) references users(id) on delete restrict;
