alter table streamers set (
    autovacuum_vacuum_scale_factor = 0.01,
    autovacuum_vacuum_threshold = 50,
    autovacuum_vacuum_cost_delay = 2
);
