-- Mano · Notas de crédito: anulan (total o parcialmente) una factura emitida.
alter table invoices add column credits uuid references invoices(id) on delete set null;
create index invoices_credits on invoices (credits) where credits is not null;
