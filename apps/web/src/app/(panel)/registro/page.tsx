import { db, tenantId } from '@/lib/db';
import { usd, when } from '@/lib/format';

type Log = { id: number; area: string; tool: string; summary: string; resolved_via: string; approved_by: string | null; cost_usd: number; created_at: string };

export default async function Registro() {
  const { data } = await db().from('actions_log').select('id,area,tool,summary,resolved_via,approved_by,cost_usd,created_at')
    .eq('tenant_id', tenantId()).order('created_at', { ascending: false }).limit(100);
  const rows = (data ?? []) as Log[];
  return (
    <>
      <h1>Registro</h1>
      <p className="sub">Todo lo que hizo Mano, quién lo aprobó y cuánto costó. No se puede editar ni borrar.</p>
      {rows.length === 0 ? <p className="empty">Todavía no hizo nada.</p> : (
        <div className="tablewrap"><table>
          <thead><tr><th>Cuándo</th><th>Área</th><th>Qué</th><th>Aprobó</th><th className="r">Costo</th></tr></thead>
          <tbody>{rows.map((r) => (
            <tr key={r.id}>
              <td>{when(r.created_at)}</td><td>{r.area}</td><td>{r.summary}</td><td>{r.approved_by ?? '—'}</td>
              <td className="r num">{Number(r.cost_usd) > 0 ? usd(Number(r.cost_usd)) : r.resolved_via}</td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
    </>
  );
}
