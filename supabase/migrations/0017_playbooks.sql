-- Mano · Playbooks por rubro: el negocio guarda su rubro y las reglas y misiones saben de qué playbook vienen
-- (al cambiar de rubro se sacan esas, nunca las del dueño).
alter table tenants add column vertical text;
alter table rules add column playbook text;
alter table missions add column playbook text;
