-- Rename model_id to channel_id and models to channels
alter table models rename column model_id to channel_id;
alter table models rename to channels;
alter table signals rename column model_id to channel_id;
alter table status_changes rename column model_id to channel_id;
alter table notification_queue rename column model_id to channel_id;
alter table referral_events rename column model_id to channel_id;

-- Rename indexes
alter index ix_signals_model_id rename to ix_signals_channel_id;
alter index ix_models_unconfirmed_online rename to ix_channels_unconfirmed_online;
alter index ix_models_model_id_is_online rename to ix_channels_channel_id_is_online;
alter index ix_status_changes_model_id_timestamp rename to ix_status_changes_channel_id_timestamp;

-- Rename constraints
alter table channels rename constraint chk_models_confirmed_status to chk_channels_confirmed_status;
alter table channels rename constraint chk_models_unconfirmed_status to chk_channels_unconfirmed_status;
alter table channels rename constraint chk_models_prev_unconfirmed_status to chk_channels_prev_unconfirmed_status;

-- Rename users.max_models to users.max_channels
alter table users rename column max_models to max_channels;
