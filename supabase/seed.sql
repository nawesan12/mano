-- Fase 0: un solo tenant (vos). Después del primer login en el panel,
-- corré el insert de members con tu auth.users.id.
insert into tenants (id, name) values ('00000000-0000-0000-0000-000000000001', 'Nahuel')
on conflict do nothing;

insert into budgets (tenant_id) values ('00000000-0000-0000-0000-000000000001')
on conflict do nothing;

insert into missions (tenant_id, area, goal, autonomy) values
  ('00000000-0000-0000-0000-000000000001', 'inbox',  'Limpiame el mail y mostrame solo lo que necesita mi decisión', 1),
  ('00000000-0000-0000-0000-000000000001', 'agenda', 'Avisame de choques y reuniones del día', 1);

-- Reglas de arranque: resuelven sin IA lo más común
insert into rules (tenant_id, priority, match, outcome, origin) values
  ('00000000-0000-0000-0000-000000000001', 10,
   '{"source":"gmail","senderIncludesAny":["noreply","no-reply","newsletter","notifications@","mailer-daemon"]}',
   '{"urgency":"ignore","note":"Automático o newsletter"}', 'playbook'),
  ('00000000-0000-0000-0000-000000000001', 20,
   '{"textIncludesAny":["cambiamos el cbu","nuevo cbu","cambio de cbu","nueva cuenta bancaria","new bank details"]}',
   '{"urgency":"critical","needsAction":true,"note":"Posible fraude: cambio de datos bancarios"}', 'playbook'),
  ('00000000-0000-0000-0000-000000000001', 30,
   '{"textIncludesAny":["intimación","embargo","carta documento","vencimiento hoy"]}',
   '{"urgency":"critical","needsAction":true,"note":"Legal o fiscal urgente"}', 'playbook');

-- insert into members (tenant_id, user_id, role, telegram_chat_id)
-- values ('00000000-0000-0000-0000-000000000001', '<tu auth.users.id>', 'owner', <tu chat id>);
