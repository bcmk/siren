drop index ix_channels_status_mismatch;

create index ix_channels_status_mismatch
on channels (channel_id)
include (unconfirmed_status, unconfirmed_timestamp, confirmed_status)
where confirmed_status != unconfirmed_status;
