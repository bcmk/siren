create table block (
    chat_id bigint not null,
    endpoint text not null,
    block integer not null,
    primary key (chat_id, endpoint)
);

create table feedback (
    chat_id bigint,
    text text,
    endpoint text not null default ''
);

create table interactions (
    priority integer not null,
    timestamp integer not null,
    endpoint text not null,
    chat_id bigint not null,
    result integer not null,
    delay integer not null,
    kind integer not null default 0
);

create index ix_interactions_endpoint on interactions (endpoint);
create index ix_interactions_timestamp on interactions ("timestamp");

create table models (
    model_id text primary key,
    status integer not null default 0,
    referred_users integer not null default 0,
    special boolean not null default false
);

create table notification_queue (
    id serial primary key,
    endpoint text not null,
    chat_id bigint not null,
    model_id text not null,
    status integer not null,
    time_diff integer,
    image_url text,
    social boolean not null default false,
    priority integer not null default 0,
    sound boolean not null default false,
    sending integer not null default 0,
    kind integer not null default 0
);

create table referrals (
    chat_id bigint primary key,
    referral_id text not null default '',
    referred_users integer not null default 0
);

create table signals (
    chat_id bigint not null,
    model_id text not null,
    endpoint text not null default '',
    confirmed integer not null default 1,
    primary key (chat_id, model_id, endpoint)
);

create index ix_signals_confirmed on signals (confirmed);

create table status_changes (
    model_id text,
    status integer not null default 0,
    timestamp integer not null default 0,
    is_latest boolean not null default false
);

create index ix_status_changes_model_id on status_changes (model_id);
create index ix_status_changes_timestamp on status_changes ("timestamp");

create unique index ix_status_changes_model_id_is_latest
on status_changes (model_id)
where is_latest = true;

create table users (
    chat_id bigint primary key,
    max_models integer not null default 0,
    reports integer not null default 0,
    blacklist boolean not null default false,
    show_images boolean not null default true,
    offline_notifications boolean not null default true
);
