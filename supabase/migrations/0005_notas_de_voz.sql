-- Mano · Notas de voz: lo importante llega como audio al WhatsApp del dueño.
alter table wa_messages drop constraint wa_messages_kind_check;
alter table wa_messages add constraint wa_messages_kind_check check (kind in ('text','interactive','template','audio'));
