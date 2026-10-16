-- Mano · WhatsApp Cloud API
-- Idempotencia del webhook, ventana de 24 h por contacto y costo real por mensaje.

-- Meta reintenta: cada evento entrante se procesa una sola vez.
create table processed_events (
  message_id    text primary key,               -- wamid entrante
  tenant_id     uuid not null references tenants(id) on delete cascade,
  received_at   timestamptz not null default now()
);

-- Contactos de WhatsApp: saber si la ventana de 24 h está abierta antes de responder libre.
create table wa_contacts (
  tenant_id       uuid not null references tenants(id) on delete cascade,
  phone           text not null,                -- wa_id, solo dígitos (E.164 sin +)
  name            text,
  is_owner        boolean not null default false,
  last_inbound_at timestamptz,
  primary key (tenant_id, phone)
);

-- Cada mensaje saliente, con la categoría y el cargo que informa Meta en el webhook de estado.
-- Desde el 1-oct-2026 los mensajes de servicio también se cobran: esta tabla es la fuente de verdad del gasto.
create table wa_messages (
  wamid         text primary key,
  tenant_id     uuid not null references tenants(id) on delete cascade,
  phone         text not null,
  kind          text not null check (kind in ('text','interactive','template')),
  template      text,
  status        text not null default 'sent',   -- sent | delivered | read | failed
  category      text,                           -- service | utility | marketing | authentication
  billable      boolean,
  error         text,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);
create index wa_messages_by_day on wa_messages (tenant_id, created_at);

do $$
declare t text;
begin
  foreach t in array array['processed_events','wa_contacts','wa_messages'] loop
    execute format('alter table %I enable row level security', t);
    execute format('create policy %I on %I for select using (is_member(tenant_id))', t || '_read', t);
  end loop;
end $$;

-- Limpieza: los ids procesados solo hacen falta unos días (Meta reintenta por horas, no semanas).
-- Programarlo con pg_cron si está disponible:
-- select cron.schedule('mano-processed-events', '0 4 * * *', $$delete from processed_events where received_at < now() - interval '7 days'$$);
