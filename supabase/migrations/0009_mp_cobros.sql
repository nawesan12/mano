-- Mano · Cobros por Mercado Pago: links de pago que Mano manda (siempre aprobados) y su seguimiento.

create table mp_charges (
  id           text primary key,               -- external_reference: mano-<id de la aprobación>
  tenant_id    uuid not null references tenants(id) on delete cascade,
  pref_id      text not null,                  -- preferencia de Checkout Pro
  phone        text,
  name         text,
  description  text not null,
  amount       numeric(14,2) not null,
  url          text not null,
  status       text not null default 'pending' check (status in ('pending','paid','expired')),
  payment_id   bigint,
  reminded_at  timestamptz,
  expires_at   timestamptz,
  paid_at      timestamptz,
  created_at   timestamptz not null default now()
);
create index mp_charges_pending on mp_charges (tenant_id, created_at) where status = 'pending';

alter table mp_charges enable row level security;
create policy mp_charges_read on mp_charges for select using (is_member(tenant_id));

-- Te pagaron un link que mandó Mano: importante (nota de voz), antes que la regla general de acreditados.
insert into rules (tenant_id, priority, match, outcome, origin)
select id, 14, '{"source":"mercadopago","kind":"payment.approved","textIncludesAny":["link de cobro de mano"]}'::jsonb,
       '{"urgency":"high","note":"Te pagaron un link de cobro"}'::jsonb, 'playbook'
from tenants;
