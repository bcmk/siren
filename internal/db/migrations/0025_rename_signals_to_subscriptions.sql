alter table signals rename to subscriptions;
alter table subscriptions rename constraint signals_pkey to subscriptions_pkey;
alter index ix_signals_confirmed rename to ix_subscriptions_confirmed;
alter index ix_signals_channel_id rename to ix_subscriptions_channel_id;
alter index ix_signals_channel_id_confirmed rename to ix_subscriptions_channel_id_confirmed;
