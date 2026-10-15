import 'server-only';
import { createClient } from '@supabase/supabase-js';

/**
 * Fase 0: panel local de un solo dueño, con service role desde el servidor.
 * Fase 1: login con Supabase Auth y lecturas con la sesión del usuario (RLS ya está lista).
 */
export function db() {
  const url = process.env.SUPABASE_URL;
  const key = process.env.SUPABASE_SERVICE_ROLE_KEY;
  if (!url || !key) throw new Error('Faltan SUPABASE_URL y SUPABASE_SERVICE_ROLE_KEY en el .env de la raíz');
  return createClient(url, key, { auth: { persistSession: false } });
}

export const tenantId = () => process.env.MANO_TENANT_ID ?? '00000000-0000-0000-0000-000000000001';
