create table referral_events (
    model_id text,
    referrer_chat_id bigint,
    follower_chat_id bigint,
    timestamp integer not null
);

insert into referral_events (model_id, timestamp)
select m.model_id, 0
from models m
cross join generate_series(1, m.referred_users)
where m.referred_users > 0;

insert into referral_events (referrer_chat_id, timestamp)
select r.chat_id, 0
from referrals r
cross join generate_series(1, r.referred_users)
where r.referred_users > 0;

create index ix_referral_events_timestamp on referral_events (timestamp);
