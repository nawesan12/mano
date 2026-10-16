// Sesión del panel en Fase 0: una contraseña (PANEL_PASSWORD) y una cookie firmada con HMAC.
// Funciona en el middleware (edge) y en el servidor: solo Web Crypto.
export const COOKIE = 'mano_s';
export const MAX_AGE = 60 * 60 * 24 * 30; // 30 días

const enc = new TextEncoder();

async function hmac(key: string, data: string): Promise<string> {
  const k = await crypto.subtle.importKey('raw', enc.encode(key), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  const sig = await crypto.subtle.sign('HMAC', k, enc.encode(data));
  return Array.from(new Uint8Array(sig), (b) => b.toString(16).padStart(2, '0')).join('');
}

function equal(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

// El token lleva la fecha de emisión: cambiar PANEL_PASSWORD invalida todas las sesiones.
export async function issue(secret: string, now = Date.now()): Promise<string> {
  const ts = String(now);
  return `${ts}.${await hmac(secret, 'mano-panel:' + ts)}`;
}

export async function verify(secret: string | undefined, token: string | undefined, now = Date.now()): Promise<boolean> {
  if (!secret || !token) return false;
  const [ts, sig] = token.split('.');
  const t = Number(ts);
  if (!sig || !Number.isFinite(t) || now - t > MAX_AGE * 1000 || t > now + 60_000) return false;
  return equal(sig, await hmac(secret, 'mano-panel:' + ts));
}

export function samePassword(a: string, b: string): boolean {
  return equal(a, b);
}
