-- Mano · Diagnóstico semanal: cuánto te ahorró y cuánto costó (sin IA, de los datos).
create table reports (
  tenant_id   uuid not null references tenants(id) on delete cascade,
  week_start  date not null,
  data        jsonb not null,
  text        text not null,
  created_at  timestamptz not null default now(),
  primary key (tenant_id, week_start)
);
alter table reports enable row level security;
create policy reports_read on reports for select using (is_member(tenant_id));
