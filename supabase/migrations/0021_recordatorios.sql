-- Mano · Recordatorios de turnos: los invitados de cada evento sirven para cruzarlo con el cliente.
alter table calendar_events add column attendees jsonb not null default '[]';
