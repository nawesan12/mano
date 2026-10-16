-- Mano · Clientes: se arman solos con mensajes, mails y pagos. El dueño suma notas.
create table customers (
  id               uuid primary key default gen_random_uuid(),
  tenant_id        uuid not null references tenants(id) on delete cascade,
  name             text,
  phone            text,
  email            text,
  contacts         int not null default 0,
  paid_total       numeric(14,2) not null default 0,
  first_seen_at    timestamptz not null default now(),
  last_contact_at  timestamptz,
  last_channel     text,
  notes            text,
  created_at       timestamptz not null default now()
);
create unique index customers_phone on customers (tenant_id, phone) where phone is not null;
create unique index customers_email on customers (tenant_id, email) where email is not null;
create index customers_recent on customers (tenant_id, last_contact_at desc);
alter table customers enable row level security;
create policy customers_read on customers for select using (is_member(tenant_id));
create policy customers_write on customers for update using (is_member(tenant_id)) with check (is_member(tenant_id));
