-- Mano · Facturación electrónica con ARCA (WSAA + WSFEv1). Factura C a consumidor final.

-- Ticket de acceso de WSAA (~12 h). Se guarda porque ARCA no da otro mientras siga vigente.
create table arca_ta (
  tenant_id   uuid primary key references tenants(id) on delete cascade,
  token       text not null,
  sign        text not null,
  expires_at  timestamptz not null
);
alter table arca_ta enable row level security; -- sin lectura desde el panel: es una credencial

create table invoices (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  label        text not null,              -- Factura C 0003-00000124
  cbte_tipo    int not null,
  number       int not null,
  cae          text not null,
  cae_expires  text not null,              -- AAAAMMDD
  customer     text,
  concept      text,
  total        numeric(14,2) not null,
  payment_ref  text,                       -- mp:<id del pago>, para no facturar dos veces
  qr_url       text,
  created_at   timestamptz not null default now()
);
create unique index invoices_payment on invoices (tenant_id, payment_ref) where payment_ref is not null;
alter table invoices enable row level security;
create policy invoices_read on invoices for select using (is_member(tenant_id));
