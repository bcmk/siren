create table schema_migrations (
    name text primary key,
    applied_at bigint not null
);

-- Only insert names when migrating from old schema_version system.
-- Fresh databases don't have schema_version table, so migrations 0001+ will run.
do $$
begin
    if exists (select 1 from information_schema.tables where table_name = 'schema_version') then
        insert into schema_migrations (name, applied_at)
        select name, extract(epoch from now())::bigint
        from (values
            ('schema_migrations'),
            ('initial_schema'),
            ('feedback_timestamp'),
            ('status_changes_index_include'),
            ('brin_indexes'),
            ('status_changes_brin_pages'),
            ('status_changes_status_is_latest_index'),
            ('online_indexes'),
            ('models_special_index'),
            ('referral_events'),
            ('rename_interactions'),
            ('received_message_log'),
            ('status_constraints'),
            ('models_unconfirmed_status_1'),
            ('models_unconfirmed_status_2'),
            ('models_unconfirmed_status_3'),
            ('signals_models_indexes'),
            ('drop_is_latest'),
            ('status_changes_index_1'),
            ('status_changes_index_2'),
            ('treat_unknown_as_offline'),
            ('drop_special'),
            ('rename_model_to_channel'),
            ('channels_indexes_rework'),
            ('rename_models_pkey'),
            ('rename_signals_to_subscriptions'),
            ('channels_status_mismatch_include'),
            ('performance_log'),
            ('notification_queue_viewers'),
            ('users_created_at'),
            ('rename_channels_indexes'),
            ('users_show_subject'),
            ('users_chat_type')
        ) as t(name);
        drop table schema_version;
    end if;
end $$;
