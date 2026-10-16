-- Mano · Seguimiento de pendientes
-- Lo que espera tu ok no se olvida: un recordatorio a las pocas horas y vencimiento a las 72 h.

alter table approvals add column reminded_at timestamptz;
create index approvals_pending on approvals (tenant_id, created_at) where status = 'pending';
