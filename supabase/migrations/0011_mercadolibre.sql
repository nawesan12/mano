-- Mano · Mercado Libre: ventas y envíos (Mercado Envíos).

-- El refresh token de Mercado Libre es de un solo uso: cada renovación trae uno nuevo y hay que guardarlo.
create table ml_auth (
  tenant_id      uuid primary key references tenants(id) on delete cascade,
  refresh_token  text not null,
  user_id        bigint,
  updated_at     timestamptz not null default now()
);

create table ml_shipments (
  tenant_id   uuid not null references tenants(id) on delete cascade,
  id          bigint not null,
  status      text not null,
  substatus   text,
  receiver    text,
  tracking    text,
  updated_at  timestamptz not null default now(),
  primary key (tenant_id, id)
);

alter table ml_auth enable row level security;            -- sin política de lectura: el token no se ve desde el panel
alter table ml_shipments enable row level security;
create policy ml_shipments_read on ml_shipments for select using (is_member(tenant_id));

-- Lo que pide acción (despachar, no entregado, cancelado) es importante; lo demás va al resumen.
insert into rules (tenant_id, priority, match, outcome, origin)
select t.id, r.priority, r.match::jsonb, r.outcome::jsonb, 'playbook'
from tenants t cross join (values
  (40, '{"source":"mercadolibre","kind":"shipment.not_delivered"}', '{"urgency":"high","note":"Un envío no se pudo entregar"}'),
  (41, '{"source":"mercadolibre","kind":"shipment.cancelled"}',     '{"urgency":"high","note":"Se canceló un envío"}'),
  (42, '{"source":"mercadolibre","kind":"shipment.ready_to_ship"}', '{"urgency":"high","note":"Tenés un envío para despachar"}'),
  (43, '{"source":"mercadolibre","kind":"order.paid"}',             '{"urgency":"high","note":"Venta nueva en Mercado Libre"}'),
  (44, '{"source":"mercadolibre","kind":"order.cancelled"}',        '{"urgency":"high","note":"Te cancelaron una venta"}'),
  (45, '{"source":"mercadolibre"}',                                 '{"urgency":"normal","note":"Movimiento en Mercado Libre"}')
) as r(priority, match, outcome);
