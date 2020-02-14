--
-- Schema initialization
--
begin;

create table player
(
    id           serial primary key,
    phone_number text not null unique,
    balance      int  not null default 0
);

create table transaction_source
(
    type text not null primary key
);

create table transaction
(
    transaction_id text primary key,
    player_id      int       not null references player (id),
    state          text      not null,
    amount         int       not null,
    created_at     timestamp not null default now(),
    cancelled      bool      not null default false,
    source         text      not null references transaction_source
);

commit;