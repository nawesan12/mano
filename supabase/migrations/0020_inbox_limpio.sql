-- Mano · Inbox limpio: lo ignorado (newsletters, automáticos) se archiva con la etiqueta Mano/Archivado.
-- Reversible: /desarchivar lo devuelve al inbox.
create table archived_mail (
  tenant_id    uuid not null references tenants(id) on delete cascade,
  message_id   text not null,
  subject      text,
  sender       text,
  restored     boolean not null default false,
  archived_at  timestamptz not null default now(),
  primary key (tenant_id, message_id)
);
create index archived_mail_recent on archived_mail (tenant_id, archived_at desc);
alter table archived_mail enable row level security;
create policy archived_mail_read on archived_mail for select using (is_member(tenant_id));
