-- Mano · Mercado Pago: reglas de arranque para pagos (sin IA).
-- Contracargo = crítico (te llama); rechazado o cobro grande = importante; lo demás va al resumen.
insert into rules (tenant_id, priority, match, outcome, origin)
select t.id, r.priority, r.match::jsonb, r.outcome::jsonb, 'playbook'
from tenants t cross join (values
  (15, '{"source":"mercadopago","kind":"payment.charged_back"}', '{"urgency":"critical","note":"Contracargo en Mercado Pago"}'),
  (16, '{"source":"mercadopago","kind":"payment.rejected"}',     '{"urgency":"high","note":"Pago rechazado en Mercado Pago"}'),
  (17, '{"source":"mercadopago","kind":"payment.approved","amountGte":500000}', '{"urgency":"high","note":"Cobro grande acreditado"}'),
  (18, '{"source":"mercadopago","kind":"payment.approved"}',     '{"urgency":"normal","note":"Pago acreditado"}'),
  (19, '{"source":"mercadopago"}',                               '{"urgency":"normal","note":"Movimiento en Mercado Pago"}')
) as r(priority, match, outcome);
