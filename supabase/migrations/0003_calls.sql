-- Mano · Llamadas de voz al dueño (WhatsApp Calling + voz en tiempo real)
-- Cuando algo es crítico, Mano te llama, te cuenta y decidís hablando.

-- Cuándo y cuánto puede llamar. Una fila por tenant; sin fila valen los defaults.
create table call_policy (
  tenant_id        uuid primary key references tenants(id) on delete cascade,
  enabled          boolean not null default true,
  quiet_from       text not null default '21:00' check (quiet_from ~ '^([01]\d|2[0-3]):[0-5]\d$'),
  quiet_to         text not null default '08:00' check (quiet_to ~ '^([01]\d|2[0-3]):[0-5]\d$'),
  max_per_day      int not null default 3 check (max_per_day between 0 and 20),
  retry_after_min  int not null default 10 check (retry_after_min between 2 and 120),
  max_attempts     int not null default 2 check (max_attempts between 1 and 3),
  updated_at       timestamptz not null default now()
);

-- Cada llamada, saliente o entrante, con su costo (Meta por minuto + IA de voz).
create table calls (
  id            text primary key,                -- wacid de Meta
  tenant_id     uuid not null references tenants(id) on delete cascade,
  direction     text not null check (direction in ('out','in')),
  event_id      uuid references events(id) on delete set null,
  approval_id   uuid references approvals(id) on delete set null,
  reason        text not null,
  status        text not null default 'dialing'
                check (status in ('dialing','ringing','accepted','rejected','missed','completed','failed')),
  attempt       int not null default 1,
  started_at    timestamptz,
  ended_at      timestamptz,
  duration_s    int not null default 0,
  wa_cost_usd   numeric(10,5) not null default 0,
  ai_cost_usd   numeric(10,5) not null default 0,
  summary       text,
  transcript    text,
  outcome       jsonb not null default '[]',     -- herramientas ejecutadas en la llamada
  created_at    timestamptz not null default now()
);
create index calls_by_day on calls (tenant_id, created_at);

-- Permiso de llamada que da el dueño en WhatsApp (sin permiso Meta rechaza la llamada).
alter table wa_contacts
  add column call_permission text not null default 'none' check (call_permission in ('none','temporary','permanent')),
  add column call_permission_until timestamptz,
  add column call_permission_requested_at timestamptz;

-- La IA de voz también pasa por el gobernador y queda en usage.
alter table usage drop constraint usage_tier_check;
alter table usage add constraint usage_tier_check check (tier in ('cheap','top','voice'));

do $$
declare t text;
begin
  foreach t in array array['call_policy','calls'] loop
    execute format('alter table %I enable row level security', t);
    execute format('create policy %I on %I for select using (is_member(tenant_id))', t || '_read', t);
  end loop;
end $$;
create policy call_policy_write on call_policy for all using (is_member(tenant_id)) with check (is_member(tenant_id));
