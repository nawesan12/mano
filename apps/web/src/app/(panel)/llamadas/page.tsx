import { db, tenantId } from '@/lib/db';
import { usd, when } from '@/lib/format';

type Call = {
  id: string; direction: string; party: string | null; reason: string; status: string; duration_s: number;
  summary: string | null; transcript: string | null; wa_cost_usd: number; ai_cost_usd: number; created_at: string;
};

const status: Record<string, string> = {
  dialing: 'Marcando', ringing: 'Sonando', accepted: 'En curso', completed: 'Atendida', missed: 'Sin atender', rejected: 'Rechazada', failed: 'Falló',
};

export default async function Llamadas() {
  const { data } = await db().from('calls').select('*').eq('tenant_id', tenantId()).order('created_at', { ascending: false }).limit(50);
  const calls = (data ?? []) as Call[];
  const cost = calls.reduce((s, c) => s + Number(c.wa_cost_usd) + Number(c.ai_cost_usd), 0);
  return (
    <>
      <h1>Llamadas</h1>
      <p className="sub">Lo crítico te llama; a clientes, solo lo que aprobaste. Últimas 50: {usd(cost)} en total.</p>
      {calls.length === 0 && <p className="empty">Todavía no hubo llamadas. Probá con /llamame.</p>}
      <div className="list">
        {calls.map((c) => (
          <article key={c.id} className={`card call ${c.status}`}>
            <span className="chip">
              {when(c.created_at)} · {c.party === 'customer' ? 'A cliente' : c.direction === 'in' ? 'Me llamaste' : 'Te llamé'} · {status[c.status] ?? c.status}
              {c.duration_s > 0 ? ` · ${Math.ceil(c.duration_s / 60)} min` : ''}
            </span>
            <b>{c.reason}</b>
            {c.summary && <p>{c.summary}</p>}
            {c.transcript && (
              <details>
                <summary>Transcripción</summary>
                <pre>{c.transcript}</pre>
              </details>
            )}
            <small>{usd(Number(c.wa_cost_usd) + Number(c.ai_cost_usd))}</small>
          </article>
        ))}
      </div>
    </>
  );
}
