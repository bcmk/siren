-- Cutover step 7, during downtime: drop the old logs, then give the timestamp indexes their names.

-- Alone in its file, and so outside a transaction, per the Locks section of docs/timescaledb.md.
-- A replay resumes, as only what is left gets dropped or renamed.
do $$
declare
    log text;
    chunk regclass;
    dropped integer := 0;
begin
    foreach log in array array['sent_message_log_old', 'received_message_log_old'] loop
        continue when to_regclass(log) is null;

        for chunk in select show_chunks(log) loop
            execute format('drop table %s', chunk);
            dropped := dropped + 1;
            if dropped % 10 = 0 then
                commit;
            end if;
        end loop;

        execute format('drop table %I', log);
        commit;
    end loop;

    alter index if exists ix_sent_message_log_new_timestamp rename to ix_sent_message_log_timestamp;
    commit;

    alter index if exists ix_received_message_log_new_timestamp rename to ix_received_message_log_timestamp;
end $$;
