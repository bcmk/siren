create table star_payments (
    id bigserial primary key,
    chat_id bigint not null,
    endpoint text not null,
    telegram_payment_charge_id text not null unique,
    stars_amount integer not null,
    product text not null,
    quantity integer not null,
    payload text not null,
    timestamp integer not null
);

create index ix_star_payments_chat_id on star_payments (chat_id);
