'use server';

import { cookies } from 'next/headers';
import { redirect } from 'next/navigation';
import { revalidatePath } from 'next/cache';
import { db, tenantId } from '@/lib/db';
import { COOKIE, MAX_AGE, issue, samePassword, verify } from '@/lib/session';

async function requireSession() {
  const jar = await cookies();
  if (!(await verify(process.env.PANEL_PASSWORD, jar.get(COOKIE)?.value))) redirect('/login');
}

export async function login(_: string | null, form: FormData): Promise<string | null> {
  const secret = process.env.PANEL_PASSWORD;
  if (!secret) return 'Falta PANEL_PASSWORD en el .env del panel.';
  if (!samePassword(String(form.get('password') ?? ''), secret)) return 'Contraseña incorrecta.';
  const jar = await cookies();
  jar.set(COOKIE, await issue(secret), { httpOnly: true, secure: process.env.NODE_ENV === 'production', sameSite: 'lax', maxAge: MAX_AGE, path: '/' });
  redirect('/');
}

export async function logout() {
  (await cookies()).delete(COOKIE);
  redirect('/login');
}

// decide manda la decisión al worker: se ejecuta en Agent.OnDecision, el mismo camino que los botones.
export async function decide(_: string | null, form: FormData): Promise<string | null> {
  await requireSession();
  const id = String(form.get('id') ?? '');
  const decision = String(form.get('decision') ?? '');
  const url = process.env.WORKER_URL, token = process.env.PANEL_TOKEN;
  if (!url || !token) return 'Configurá WORKER_URL y PANEL_TOKEN para decidir desde el panel.';
  try {
    const res = await fetch(`${url.replace(/\/$/, '')}/api/decide`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
      body: JSON.stringify({ id, decision }),
      cache: 'no-store',
    });
    if (!res.ok) return `El worker respondió ${res.status}.`;
    const out = (await res.json()) as { message?: string };
    revalidatePath('/');
    return out.message ?? 'Hecho.';
  } catch {
    return 'No pude hablar con el worker. ¿Está corriendo?';
  }
}

export async function addFact(form: FormData) {
  await requireSession();
  const topic = String(form.get('topic') ?? '').trim().slice(0, 60);
  const fact = String(form.get('fact') ?? '').trim().slice(0, 500);
  if (!topic || !fact) return;
  await db().from('memory_facts').upsert({ tenant_id: tenantId(), topic, fact, updated_at: new Date().toISOString() }, { onConflict: 'tenant_id,topic,fact' });
  revalidatePath('/memoria');
}

export async function deleteFact(form: FormData) {
  await requireSession();
  await db().from('memory_facts').delete().eq('tenant_id', tenantId()).eq('id', String(form.get('id') ?? ''));
  revalidatePath('/memoria');
}

export async function toggleRule(form: FormData) {
  await requireSession();
  const active = form.get('active') === 'true';
  await db().from('rules').update({ active: !active }).eq('tenant_id', tenantId()).eq('id', String(form.get('id') ?? ''));
  revalidatePath('/reglas');
}

export async function addCustomerNote(form: FormData) {
  await requireSession();
  const id = String(form.get('id') ?? '');
  const note = String(form.get('note') ?? '').trim().slice(0, 300);
  if (!id || !note) return;
  const sb = db();
  const { data } = await sb.from('customers').select('notes').eq('tenant_id', tenantId()).eq('id', id).maybeSingle();
  const day = new Date().toLocaleDateString('es-AR', { day: '2-digit', month: '2-digit', timeZone: 'America/Argentina/Buenos_Aires' });
  const notes = [data?.notes, `${day} ${note}`].filter(Boolean).join('\n').slice(-4000);
  await sb.from('customers').update({ notes }).eq('tenant_id', tenantId()).eq('id', id);
  revalidatePath('/clientes');
}
