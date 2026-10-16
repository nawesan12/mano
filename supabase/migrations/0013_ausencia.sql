-- Mano · Modo ausencia: solo llama por lo crítico; a los clientes les responde una vez con el texto que aprobaste.
create table away (
  tenant_id       uuid primary key references tenants(id) on delete cascade,
  until_at        timestamptz not null,
  started_at      timestamptz not null default now(),
  reply           text not null,
  reply_approved  boolean not null default false,
  active          boolean not null default true
);

create table away_replies (
  tenant_id  uuid not null references tenants(id) on delete cascade,
  phone      text not null,
  at         timestamptz not null default now(),
  primary key (tenant_id, phone)
);

do $$
declare t text;
begin
  foreach t in array array['away','away_replies'] loop
    execute format('alter table %I enable row level security', t);
    execute format('create policy %I on %I for select using (is_member(tenant_id))', t || '_read', t);
  end loop;
end $$;
