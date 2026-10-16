-- Mano · Transferencias salientes por Mercado Pago (Payouts). Apagadas por defecto:
-- requieren habilitación de Mercado Pago, clave Ed25519 registrada y aprobación con botón de cada una.
create table mp_payouts (
  id           text primary key,                -- mano-payout-<id de la aprobación> (también idempotencia)
  tenant_id    uuid not null references tenants(id) on delete cascade,
  payout_id    text,                            -- POP… de Mercado Pago
  destination  text not null,                   -- mail o titular + CBU enmascarado
  amount       numeric(14,2) not null,
  reason       text not null,
  status       text not null,
  final        boolean not null default false,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);
create index mp_payouts_by_day on mp_payouts (tenant_id, created_at);
alter table mp_payouts enable row level security;
create policy mp_payouts_read on mp_payouts for select using (is_member(tenant_id));
