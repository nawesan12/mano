-- Mano · Cobranza escalonada: 0 nada · 1 se propuso recordarle por WhatsApp (día 3) · 2 se propuso llamarlo (día 6).
alter table mp_charges add column stage int not null default 0 check (stage between 0 and 2);
