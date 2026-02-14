alter table channels rename to streamers;
alter table streamers rename constraint channels_pkey to streamers_pkey;

alter table streamers rename column channel_id to streamer_id;
alter table subscriptions rename column channel_id to streamer_id;
alter table status_changes rename column channel_id to streamer_id;
alter table notification_queue rename column channel_id to streamer_id;
alter table referral_events rename column channel_id to streamer_id;

alter table users rename column max_channels to max_subs;

alter table streamers rename constraint chk_channels_confirmed_status to chk_streamers_confirmed_status;
alter table streamers rename constraint chk_channels_unconfirmed_status to chk_streamers_unconfirmed_status;
alter table streamers rename constraint chk_channels_prev_unconfirmed_status to chk_streamers_prev_unconfirmed_status;

alter index ix_channels_channel_id_online rename to ix_streamers_streamer_id_online;
alter index ix_channels_channel_id_status_mismatch rename to ix_streamers_streamer_id_status_mismatch;
alter index ix_subscriptions_channel_id rename to ix_subscriptions_streamer_id;
alter index ix_subscriptions_channel_id_confirmed rename to ix_subscriptions_streamer_id_confirmed;
alter index ix_status_changes_channel_id_timestamp rename to ix_status_changes_streamer_id_timestamp;
