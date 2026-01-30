drop index ix_channels_channel_id_is_online;

create index ix_signals_channel_id_confirmed
on signals (channel_id)
where confirmed = 1;

create index ix_channels_status_mismatch
on channels (channel_id)
include (unconfirmed_status, unconfirmed_timestamp)
where confirmed_status != unconfirmed_status;

alter table channels drop constraint chk_channels_confirmed_status;
alter table channels add constraint chk_channels_confirmed_status check (confirmed_status in (0, 1, 2));
alter table channels alter column confirmed_status set default 0;
update channels set confirmed_status = 0 where unconfirmed_status = 0;
