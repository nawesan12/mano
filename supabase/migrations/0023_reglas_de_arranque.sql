-- Mano · Reglas de arranque de las integraciones en una función.
-- 0006, 0007 y 0011 solo cargaban reglas para los tenants que ya existían: en una instalación nueva
-- (el tenant lo crea seed.sql, después) quedaban sin reglas. Ahora el seed también las carga.
-- Es idempotente: no duplica una regla de arranque que ya está.
create or replace function mano_reglas_de_arranque(t uuid) returns void language sql as $$
  insert into rules (tenant_id, priority, match, outcome, origin)
  select t, r.priority, r.match::jsonb, r.outcome::jsonb, 'playbook'
  from (values
    -- Mercado Pago
    (14, '{"source":"mercadopago","kind":"payment.approved","textIncludesAny":["link de cobro de mano"]}', '{"urgency":"high","note":"Te pagaron un link de cobro"}'),
    (15, '{"source":"mercadopago","kind":"payment.charged_back"}', '{"urgency":"critical","note":"Contracargo en Mercado Pago"}'),
    (16, '{"source":"mercadopago","kind":"payment.rejected"}',     '{"urgency":"high","note":"Pago rechazado en Mercado Pago"}'),
    (17, '{"source":"mercadopago","kind":"payment.approved","amountGte":500000}', '{"urgency":"high","note":"Cobro grande acreditado"}'),
    (18, '{"source":"mercadopago","kind":"payment.approved"}',     '{"urgency":"normal","note":"Pago acreditado"}'),
    (19, '{"source":"mercadopago"}',                               '{"urgency":"normal","note":"Movimiento en Mercado Pago"}'),
    -- Google Calendar
    (25, '{"source":"calendar","kind":"calendar.cancelled"}', '{"urgency":"high","note":"Te cancelaron una reunión"}'),
    (26, '{"source":"calendar","kind":"calendar.moved"}',     '{"urgency":"high","note":"Te movieron una reunión"}'),
    (27, '{"source":"calendar","kind":"calendar.conflict"}',  '{"urgency":"high","note":"Choque en tu agenda"}'),
    (28, '{"source":"calendar","kind":"calendar.invite"}',    '{"urgency":"normal","note":"Invitación nueva"}'),
    -- Mercado Libre
    (40, '{"source":"mercadolibre","kind":"shipment.not_delivered"}', '{"urgency":"high","note":"Un envío no se pudo entregar"}'),
    (41, '{"source":"mercadolibre","kind":"shipment.cancelled"}',     '{"urgency":"high","note":"Se canceló un envío"}'),
    (42, '{"source":"mercadolibre","kind":"shipment.ready_to_ship"}', '{"urgency":"high","note":"Tenés un envío para despachar"}'),
    (43, '{"source":"mercadolibre","kind":"order.paid"}',             '{"urgency":"high","note":"Venta nueva en Mercado Libre"}'),
    (44, '{"source":"mercadolibre","kind":"order.cancelled"}',        '{"urgency":"high","note":"Te cancelaron una venta"}'),
    (45, '{"source":"mercadolibre"}',                                 '{"urgency":"normal","note":"Movimiento en Mercado Libre"}')
  ) as r(priority, match, outcome)
  where not exists (select 1 from rules x where x.tenant_id = t and x.origin = 'playbook' and x.match = r.match::jsonb);
$$;

do $$ begin perform mano_reglas_de_arranque(id) from tenants; end $$;
