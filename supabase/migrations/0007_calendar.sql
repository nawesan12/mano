-- Mano · Google Calendar: copia local para detectar cambios y reglas de arranque.

create table calendar_state (
  tenant_id   uuid primary key references tenants(id) on delete cascade,
  sync_token  text,
  updated_at  timestamptz not null default now()
);

-- Solo lo que puede importar (de ayer a dos semanas): sirve para saber qué movieron y armar la agenda.
create table calendar_events (
  tenant_id     uuid not null references tenants(id) on delete cascade,
  id            text not null,
  summary       text,
  start_at      timestamptz not null,
  end_at        timestamptz not null,
  all_day       boolean not null default false,
  status        text not null default 'confirmed',
  organizer     text,
  organizer_me  boolean not null default false,
  busy          boolean not null default true,
  updated_at    timestamptz not null default now(),
  primary key (tenant_id, id)
);
create index calendar_events_by_start on calendar_events (tenant_id, start_at);

do $$
declare t text;
begin
  foreach t in array array['calendar_state','calendar_events'] loop
    execute format('alter table %I enable row level security', t);
    execute format('create policy %I on %I for select using (is_member(tenant_id))', t || '_read', t);
  end loop;
end $$;

-- Lo que otro movió, canceló o pisa tu agenda es importante (nota de voz); una invitación va al resumen.
insert into rules (tenant_id, priority, match, outcome, origin)
select t.id, r.priority, r.match::jsonb, r.outcome::jsonb, 'playbook'
from tenants t cross join (values
  (25, '{"source":"calendar","kind":"calendar.cancelled"}', '{"urgency":"high","note":"Te cancelaron una reunión"}'),
  (26, '{"source":"calendar","kind":"calendar.moved"}',     '{"urgency":"high","note":"Te movieron una reunión"}'),
  (27, '{"source":"calendar","kind":"calendar.conflict"}',  '{"urgency":"high","note":"Choque en tu agenda"}'),
  (28, '{"source":"calendar","kind":"calendar.invite"}',    '{"urgency":"normal","note":"Invitación nueva"}')
) as r(priority, match, outcome);
