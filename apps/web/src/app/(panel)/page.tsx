import Decide from '@/components/Decide';
import { db, tenantId } from '@/lib/db';

export const dynamic = 'force-dynamic';

type Approval = { id: string; area: string; risk: string; title: string; detail: string | null; action: { type?: string } | null };
type Call = { id: string; reason: string; status: string; duration_s: number; summary: string | null; wa_cost_usd: number; ai_cost_usd: number; created_at: string };
type Log = { id: number; area: string; summary: string; resolved_via: string; cost_usd: number; created_at: string };

const usd = (n: number) => `USD ${n.toFixed(n > 0 && n < 0.1 ? 3 : 2)}`;
const callStatus: Record<string, string> = {
  dialing: 'Marcando', ringing: 'Sonando', accepted: 'En curso', completed: 'Atendida', missed: 'Sin atender', rejected: 'La cortaste', failed: 'Falló',
};
const hhmm = (s: string) => new Date(s).toLocaleTimeString('es-AR', { hour: '2-digit', minute: '2-digit', timeZone: 'America/Argentina/Buenos_Aires' });

export default async function Home() {
  const sb = db();
  const t = tenantId();
  const since = new Date(); since.setHours(0, 0, 0, 0);

  const [approvals, log, spend, budget, events, calls, policy] = await Promise.all([
    sb.from('approvals').select('id,area,risk,title,detail,action').eq('tenant_id', t).eq('status', 'pending').order('created_at'),
    sb.from('actions_log').select('id,area,summary,resolved_via,cost_usd,created_at').eq('tenant_id', t).order('created_at', { ascending: false }).limit(20),
    sb.from('spend_summary').select('day_usd,month_usd').eq('tenant_id', t).maybeSingle(),
    sb.from('budgets').select('cap_day_usd,cap_month_usd').eq('tenant_id', t).maybeSingle(),
    sb.from('events').select('triaged_via').eq('tenant_id', t).gte('received_at', since.toISOString()),
    sb.from('calls').select('id,reason,status,duration_s,summary,wa_cost_usd,ai_cost_usd,created_at').eq('tenant_id', t).order('created_at', { ascending: false }).limit(5),
    sb.from('call_policy').select('max_per_day,enabled').eq('tenant_id', t).maybeSingle(),
  ]);

  const pend = (approvals.data ?? []) as Approval[];
  const rows = (log.data ?? []) as Log[];
  const day = Number(spend.data?.day_usd ?? 0), month = Number(spend.data?.month_usd ?? 0);
  const capDay = Number(budget.data?.cap_day_usd ?? 3), capMonth = Number(budget.data?.cap_month_usd ?? 40);
  const ev = (events.data ?? []) as { triaged_via: string | null }[];
  const callRows = (calls.data ?? []) as Call[];
  const callsToday = callRows.filter((c) => new Date(c.created_at) >= since).length;
  const maxCalls = Number(policy.data?.max_per_day ?? 3);
  const callsOn = policy.data?.enabled ?? true;
  const free = ev.length ? Math.round((ev.filter((e) => e.triaged_via === 'rule').length / ev.length) * 100) : 0;

  return (
    <>
      <h1>Hola. Esto hizo <em>Mano</em>.</h1>
      <p className="sub">Decidí acá, en Telegram, en WhatsApp o en una llamada: todo pasa por el mismo lugar.</p>

      <section className="stats">
        <div className="stat"><small>Esperan tu ok</small><b>{pend.length}</b></div>
        <div className="stat"><small>Eventos hoy</small><b>{ev.length}</b></div>
        <div className="stat"><small>Resueltos sin IA</small><b>{free}%</b></div>
        <div className="stat"><small>Llamadas hoy</small><b>{callsToday}</b><small>{callsOn ? `tope ${maxCalls}` : 'apagadas'}</small></div>
        <div className="stat"><small>Gasto hoy</small><b>{usd(day)}</b><small>tope {usd(capDay)}</small></div>
        <div className="stat"><small>Gasto del mes</small><b>{usd(month)}</b><small>tope {usd(capMonth)}</small></div>
      </section>

      <div className="cols">
        <section className="card">
          <h2>Necesita tu ok</h2>
          {pend.length === 0 && <p className="empty">Nada pendiente.</p>}
          {pend.map((a) => (
            <div key={a.id} className={`appr ${a.risk}`}>
              <span className="chip">{a.area} · riesgo {a.risk}</span>
              <div><b>{a.title}</b></div>
              {a.detail && <p>{a.detail.slice(0, 400)}</p>}
              <Decide id={a.id} canIgnore={a.action?.type === 'gmail.reply' || a.action?.type === 'whatsapp.reply'} />
            </div>
          ))}
          <h2 className="gap">Llamadas</h2>
          {callRows.length === 0 && <p className="empty">Todavía no te llamó. Probá con /llamame.</p>}
          {callRows.map((c) => (
            <div key={c.id} className={`call ${c.status}`}>
              <span className="chip">{hhmm(c.created_at)} · {callStatus[c.status] ?? c.status}{c.duration_s > 0 ? ` · ${Math.ceil(c.duration_s / 60)} min` : ''}</span>
              <div><b>{c.reason}</b></div>
              {c.summary && <p>{c.summary}</p>}
              {Number(c.wa_cost_usd) + Number(c.ai_cost_usd) > 0 && <small>{usd(Number(c.wa_cost_usd) + Number(c.ai_cost_usd))}</small>}
            </div>
          ))}
        </section>
        <section className="card">
          <h2>Registro</h2>
          {rows.length === 0 && <p className="empty">Todavía no hizo nada. Escribile al bot.</p>}
          {rows.map((r) => (
            <div key={r.id} className="row">
              <time>{hhmm(r.created_at)}</time>
              <span>{r.summary}</span>
              <span>{r.cost_usd > 0 ? usd(Number(r.cost_usd)) : r.resolved_via}</span>
            </div>
          ))}
        </section>
      </div>
    </>
  );
}
