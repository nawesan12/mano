-- Mano · Fase 0 · modelo de datos del núcleo
-- Todo es un evento que entra, una misión que lo interpreta y una acción que queda registrada.

create extension if not exists vector;
create extension if not exists pgcrypto;

-- ─── Tenancy ───────────────────────────────────────────────
create table tenants (
  id            uuid primary key default gen_random_uuid(),
  name          text not null,
  country       text not null default 'AR',
  currency      text not null default 'ARS',
  timezone      text not null default 'America/Argentina/Buenos_Aires',
  plan          text not null default 'pro' check (plan in ('solo','pro','equipo','byok_solo','byok_pro','byok_equipo')),
  ai_mode       text not null default 'included' check (ai_mode in ('included','byok')),
  created_at    timestamptz not null default now()
);

create table members (
  tenant_id     uuid not null references tenants(id) on delete cascade,
  user_id       uuid not null references auth.users(id) on delete cascade,
  role          text not null default 'owner' check (role in ('owner','admin','member','accountant')),
  telegram_chat_id bigint,
  phone         text,
  primary key (tenant_id, user_id)
);

-- ─── Conexiones ────────────────────────────────────────────
create table connections (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  provider      text not null,                 -- gmail, gcal, whatsapp, mercadopago, arca, telegram…
  account       text,                          -- email, número, CUIT
  scopes        jsonb not null default '{}',   -- {"read":true,"draft":true,"send":false}
  secret_enc    bytea,                         -- token cifrado (pgsodium / KMS en Fase 1)
  status        text not null default 'active' check (status in ('active','error','revoked')),
  created_at    timestamptz not null default now(),
  unique (tenant_id, provider, account)
);

-- ─── Misiones y reglas ─────────────────────────────────────
create table missions (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  area          text not null,                 -- cobros, ventas, atencion, agenda, inbox, fiscal
  goal          text not null,                 -- "Que no se me caiga ningún cobro"
  metric        text,
  autonomy      smallint not null default 1 check (autonomy between 0 and 3),
  active        boolean not null default true,
  created_at    timestamptz not null default now()
);

create table rules (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  priority      int not null default 100,      -- menor = se evalúa antes
  match         jsonb not null,                -- ver packages/core/src/rules.ts
  outcome       jsonb not null,
  origin        text not null default 'owner' check (origin in ('owner','learned','playbook')),
  hits          int not null default 0,
  active        boolean not null default true,
  created_at    timestamptz not null default now()
);

-- ─── Flujo de trabajo ──────────────────────────────────────
create table events (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  source        text not null,
  external_id   text not null,                 -- id del mensaje/pago en el origen (dedupe)
  kind          text not null,
  sender        text,
  subject       text,
  body          text,
  amount        numeric(14,2),
  payload       jsonb not null default '{}',
  urgency       text check (urgency in ('critical','high','normal','ignore')),
  triaged_via   text check (triaged_via in ('rule','classifier','top')),
  summary       text,
  briefed_at    timestamptz,
  received_at   timestamptz not null default now(),
  unique (tenant_id, source, external_id)
);
create index events_unbriefed on events (tenant_id, received_at) where briefed_at is null;

create type task_state as enum
  ('received','filtered','classified','planned','awaiting_approval','executing','verifying','done','failed','escalated');

create table tasks (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  event_id      uuid references events(id) on delete set null,
  mission_id    uuid references missions(id) on delete set null,
  state         task_state not null default 'received',
  title         text not null,
  plan          jsonb,
  result        jsonb,
  cost_usd      numeric(10,5) not null default 0,
  attempts      int not null default 0,
  updated_at    timestamptz not null default now(),
  created_at    timestamptz not null default now()
);

create table approvals (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  task_id       uuid references tasks(id) on delete cascade,
  area          text not null,
  risk          text not null default 'medio' check (risk in ('bajo','medio','alto')),
  title         text not null,
  detail        text,
  action        jsonb not null,                -- {"type":"gmail.reply","threadId":"…","body":"…"}
  channel       text not null default 'telegram',
  status        text not null default 'pending' check (status in ('pending','approved','rejected','expired')),
  decided_by    text,
  decided_at    timestamptz,
  expires_at    timestamptz,
  created_at    timestamptz not null default now()
);

-- Registro inmutable de todo lo que Mano hizo
create table actions_log (
  id            bigint generated always as identity primary key,
  tenant_id     uuid not null references tenants(id) on delete cascade,
  task_id       uuid references tasks(id) on delete set null,
  area          text not null,
  tool          text not null,
  summary       text not null,
  resolved_via  text not null,                 -- rule | cheap | top | code
  model         text,
  approved_by   text,                          -- 'auto' | 'owner' | nivel de autonomía
  cost_usd      numeric(10,5) not null default 0,
  created_at    timestamptz not null default now()
);
create or replace function forbid_update() returns trigger language plpgsql as $$
begin raise exception 'actions_log es de solo inserción'; end $$;
create trigger actions_log_immutable before update or delete on actions_log
  for each row execute function forbid_update();

-- ─── Memoria ───────────────────────────────────────────────
create table memory_facts (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  topic         text not null,                 -- "Gómez", "Honorarios", "Tono"
  fact          text not null,
  source        text not null default 'owner', -- owner | learned | import
  updated_at    timestamptz not null default now(),
  unique (tenant_id, topic, fact)
);

create table memory_chunks (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  source        text not null,
  ref           text,
  content       text not null,
  embedding     vector(1024),
  created_at    timestamptz not null default now()
);
create index memory_chunks_embedding on memory_chunks using hnsw (embedding vector_cosine_ops);

-- ─── Gasto ─────────────────────────────────────────────────
create table budgets (
  tenant_id     uuid primary key references tenants(id) on delete cascade,
  cap_day_usd   numeric(10,2) not null default 3,
  cap_month_usd numeric(10,2) not null default 40
);

create table usage (
  id            bigint generated always as identity primary key,
  tenant_id     uuid not null references tenants(id) on delete cascade,
  task_id       uuid references tasks(id) on delete set null,
  tier          text not null check (tier in ('cheap','top')),
  model         text not null,
  input_tokens  int not null default 0,
  output_tokens int not null default 0,
  cost_usd      numeric(10,5) not null,
  created_at    timestamptz not null default now()
);
create index usage_by_day on usage (tenant_id, created_at);

create or replace view spend_summary as
select tenant_id,
  coalesce(sum(cost_usd) filter (where created_at >= date_trunc('day', now())), 0)   as day_usd,
  coalesce(sum(cost_usd) filter (where created_at >= date_trunc('month', now())), 0) as month_usd,
  coalesce(sum(cost_usd) filter (where created_at >= now() - interval '1 hour'), 0)  as hour_usd
from usage group by tenant_id;

-- ─── Seguridad: RLS por tenant ─────────────────────────────
-- El worker usa la service role (salta RLS). El panel usa la sesión del usuario.
create or replace function is_member(t uuid) returns boolean
language sql stable security definer set search_path = public as $$
  select exists (select 1 from members m where m.tenant_id = t and m.user_id = auth.uid())
$$;

do $$
declare t text;
begin
  foreach t in array array['connections','missions','rules','events','tasks','approvals',
                           'actions_log','memory_facts','memory_chunks','budgets','usage']
  loop
    execute format('alter table %I enable row level security', t);
    execute format('create policy %I on %I for select using (is_member(tenant_id))', t || '_read', t);
  end loop;
end $$;

alter table tenants enable row level security;
create policy tenants_read on tenants for select using (is_member(id));
alter table members enable row level security;
create policy members_read on members for select using (user_id = auth.uid());

-- El dueño decide aprobaciones, ajusta misiones, memoria y topes desde el panel
create policy approvals_decide on approvals for update using (is_member(tenant_id)) with check (is_member(tenant_id));
create policy missions_write on missions for all using (is_member(tenant_id)) with check (is_member(tenant_id));
create policy facts_write on memory_facts for all using (is_member(tenant_id)) with check (is_member(tenant_id));
create policy budgets_write on budgets for update using (is_member(tenant_id)) with check (is_member(tenant_id));
