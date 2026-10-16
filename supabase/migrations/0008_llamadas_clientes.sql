-- Mano · Llamadas a clientes (siempre aprobadas por el dueño).
-- party separa las llamadas al dueño (cuentan para su tope diario) de las llamadas a clientes.
alter table calls add column party text not null default 'owner' check (party in ('owner','customer'));
