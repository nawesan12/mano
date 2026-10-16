-- Mano · Antifraude: cuentas bancarias conocidas por remitente.
-- La primera que pasa un remitente queda de confianza; una distinta después es crítica hasta que el dueño la confirme.
create table known_accounts (
  tenant_id  uuid not null references tenants(id) on delete cascade,
  sender     text not null,          -- mail:<dirección> | wa:<teléfono>
  account    text not null,          -- cbu:<22 dígitos> | alias:<alias>
  trusted    boolean not null default false,
  seen_at    timestamptz not null default now(),
  primary key (tenant_id, sender, account)
);
alter table known_accounts enable row level security;
create policy known_accounts_read on known_accounts for select using (is_member(tenant_id));
