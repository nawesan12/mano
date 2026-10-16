import { db, tenantId } from '@/lib/db';
import { addMission, setMission } from '../../actions';

type Mission = { id: string; area: string; goal: string; autonomy: number; active: boolean };

const levels = [
  { n: 0, label: 'Observa', hint: 'Detecta y te cuenta' },
  { n: 1, label: 'Prepara', hint: 'Deja todo listo para aprobar' },
  { n: 2, label: 'Ejecuta y avisa', hint: 'Hace lo reversible y te avisa' },
  { n: 3, label: 'Piloto automático', hint: 'Todo dentro de los límites' },
];

export default async function Misiones() {
  const { data } = await db().from('missions').select('id,area,goal,autonomy,active').eq('tenant_id', tenantId()).order('created_at');
  const missions = (data ?? []) as Mission[];
  return (
    <>
      <h1>Misiones</h1>
      <p className="sub">Lo que Mano persigue por su cuenta. El nivel dice cuánto hace sin preguntarte; lo que mueve plata o habla por vos siempre pide tu ok.</p>
      <div className="list">
        {missions.map((m) => (
          <article key={m.id} className={`card mission${m.active ? '' : ' off'}`}>
            <span className="chip">{m.area}</span>
            <b>{m.goal}</b>
            <form action={setMission} className="seg" aria-label={`Nivel de autonomía de ${m.goal}`}>
              <input type="hidden" name="id" value={m.id} />
              {levels.map((l) => (
                <button key={l.n} name="autonomy" value={l.n} aria-pressed={m.autonomy === l.n} title={l.hint}>{l.label}</button>
              ))}
            </form>
            <form action={setMission}>
              <input type="hidden" name="id" value={m.id} />
              <input type="hidden" name="active" value={String(!m.active)} />
              <button className="linkbtn">{m.active ? 'Pausar' : 'Reactivar'}</button>
            </form>
          </article>
        ))}
        {missions.length === 0 && <p className="empty">Sin misiones. Elegí tu rubro con /negocio y te propongo las primeras.</p>}
      </div>
      <h2 className="gap">Nueva misión</h2>
      <form action={addMission} className="card addfact">
        <input name="area" placeholder="Área (ej. cobros)" maxLength={30} required aria-label="Área" />
        <input name="goal" placeholder="Objetivo (ej. que no se me caiga ningún cobro)" maxLength={160} required aria-label="Objetivo" />
        <button className="btn primary">Agregar</button>
      </form>
    </>
  );
}
