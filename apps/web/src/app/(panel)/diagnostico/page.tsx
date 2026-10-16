import { db, tenantId } from '@/lib/db';
import { ars, usd } from '@/lib/format';

type Data = {
  Events: number; NoAI: number; Critical: number; High: number; Replies: number; Calls: number; CustomerCalls: number;
  ChargesPaid: number; Collected: number; FraudAlerts: number; AICostUSD: number; WACostUSD: number; SavedMinutes: number;
};
type Row = { week_start: string; text: string; data: Data };

const hm = (m: number) => (m >= 60 ? `${Math.floor(m / 60)} h ${Math.round(m % 60)} min` : `${Math.round(m)} min`);

export default async function Diagnostico() {
  const { data } = await db().from('reports').select('week_start,text,data').eq('tenant_id', tenantId()).order('week_start', { ascending: false }).limit(8);
  const rows = (data ?? []) as Row[];
  const last = rows[0];
  return (
    <>
      <h1>Diagnóstico</h1>
      <p className="sub">Cada lunes: cuánto tiempo te ahorró Mano y cuánto costó. Sale de los datos, sin IA. Pedilo cuando quieras con /diagnostico.</p>
      {!last ? <p className="empty">Todavía no hay diagnóstico. Mandale /diagnostico a Mano.</p> : (
        <>
          <section className="stats">
            <div className="stat"><small>Tiempo ahorrado</small><b>{hm(last.data.SavedMinutes)}</b></div>
            <div className="stat"><small>Resuelto sin IA</small><b>{last.data.Events ? Math.round((last.data.NoAI * 100) / last.data.Events) : 0}%</b></div>
            <div className="stat"><small>Cobrado por links</small><b>{ars(last.data.Collected)}</b></div>
            <div className="stat"><small>Costo</small><b>{usd(last.data.AICostUSD + last.data.WACostUSD)}</b></div>
          </section>
          <article className="card gap"><p style={{ whiteSpace: 'pre-line' }}>{last.text}</p></article>
          {rows.length > 1 && (
            <>
              <h2 className="gap">Semanas anteriores</h2>
              <div className="tablewrap"><table>
                <thead><tr><th>Semana</th><th className="r">Entraron</th><th className="r">Sin IA</th><th className="r">Ahorro</th><th className="r">Costo</th></tr></thead>
                <tbody>{rows.map((r) => (
                  <tr key={r.week_start}>
                    <td>{new Date(r.week_start + 'T12:00:00').toLocaleDateString('es-AR', { day: 'numeric', month: 'short' })}</td>
                    <td className="r num">{r.data.Events}</td>
                    <td className="r num">{r.data.Events ? Math.round((r.data.NoAI * 100) / r.data.Events) : 0}%</td>
                    <td className="r num">{hm(r.data.SavedMinutes)}</td>
                    <td className="r num">{usd(r.data.AICostUSD + r.data.WACostUSD)}</td>
                  </tr>
                ))}</tbody>
              </table></div>
            </>
          )}
        </>
      )}
    </>
  );
}
