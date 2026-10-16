import { db, tenantId } from '@/lib/db';
import { toggleRule } from '../../actions';

type Rule = {
  id: string; priority: number; origin: string; hits: number; active: boolean;
  match: { source?: string; kind?: string; senderIncludesAny?: string[]; textIncludesAny?: string[]; amountGte?: number };
  outcome: { urgency: string; notify?: string; ignoreQuiet?: boolean; note?: string };
};

const urgency: Record<string, string> = { critical: 'Crítico', high: 'Importante', normal: 'Al resumen', ignore: 'Ignorar' };
const origin: Record<string, string> = { owner: 'Tuya', learned: 'Aprendida', playbook: 'De arranque' };

// describe pasa el JSON de la regla a una frase.
function describe(m: Rule['match']): string {
  const parts: string[] = [];
  if (m.source) parts.push(`de ${m.source}`);
  if (m.kind) parts.push(m.kind.replace('.', ': '));
  if (m.senderIncludesAny?.length) parts.push(`si escribe ${m.senderIncludesAny.join(' o ')}`);
  if (m.textIncludesAny?.length) parts.push(`si dice "${m.textIncludesAny.slice(0, 3).join('", "')}"${m.textIncludesAny.length > 3 ? '…' : ''}`);
  if (m.amountGte) parts.push(`desde $${m.amountGte.toLocaleString('es-AR')}`);
  return parts.join(' · ') || 'todo';
}

export default async function Reglas() {
  const { data } = await db().from('rules').select('id,priority,origin,hits,active,match,outcome').eq('tenant_id', tenantId()).order('priority');
  const rules = (data ?? []) as Rule[];
  return (
    <>
      <h1>Reglas</h1>
      <p className="sub">Resuelven sin IA, gratis. Sumá las tuyas hablándole a Mano: &quot;llamame si escribe Liliana&quot;.</p>
      <div className="tablewrap"><table>
        <thead><tr><th>Cuándo</th><th>Qué hace</th><th>Origen</th><th className="r">Usos</th><th></th></tr></thead>
        <tbody>{rules.map((r) => (
          <tr key={r.id} className={r.active ? '' : 'off'}>
            <td>{describe(r.match)}{r.outcome.note && <small className="note">{r.outcome.note}</small>}</td>
            <td>
              {urgency[r.outcome.urgency] ?? r.outcome.urgency}
              {r.outcome.notify === 'call' && ' · te llama'}{r.outcome.notify === 'silent' && ' · sin aviso'}{r.outcome.ignoreQuiet && ' · aun de noche'}
            </td>
            <td>{origin[r.origin] ?? r.origin}</td>
            <td className="r num">{r.hits}</td>
            <td>
              <form action={toggleRule}>
                <input type="hidden" name="id" value={r.id} />
                <input type="hidden" name="active" value={String(r.active)} />
                <button className="btn">{r.active ? 'Pausar' : 'Activar'}</button>
              </form>
            </td>
          </tr>
        ))}</tbody>
      </table></div>
    </>
  );
}
