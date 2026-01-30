create index ix_signals_model_id on signals (model_id);
create index ix_models_unconfirmed_online on models (model_id) where unconfirmed_status = 2;
