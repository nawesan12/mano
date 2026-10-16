import { db, tenantId } from '@/lib/db';
import { addFact, deleteFact } from '../../actions';

type Fact = { id: string; topic: string; fact: string; source: string };

export default async function Memoria() {
  const { data } = await db().from('memory_facts').select('id,topic,fact,source').eq('tenant_id', tenantId()).order('topic');
  const facts = (data ?? []) as Fact[];
  return (
    <>
      <h1>Memoria</h1>
      <p className="sub">Lo que Mano sabe de tu negocio. Lo usa para redactar y para hablar en las llamadas.</p>
      <form action={addFact} className="card addfact">
        <input name="topic" placeholder="Tema (ej. Honorarios)" maxLength={60} required aria-label="Tema" />
        <input name="fact" placeholder="Dato (ej. 3% más IVA)" maxLength={500} required aria-label="Dato" />
        <button className="btn primary">Agregar</button>
      </form>
      {facts.length === 0 && <p className="empty">Todavía no sabe nada. Contale cosas: &quot;acordate que…&quot;.</p>}
      <div className="list">
        {facts.map((f) => (
          <div key={f.id} className="row fact">
            <b>{f.topic}</b>
            <span>{f.fact}</span>
            <form action={deleteFact}>
              <input type="hidden" name="id" value={f.id} />
              <button className="linkbtn" aria-label={`Olvidar ${f.topic}`}>Olvidar</button>
            </form>
          </div>
        ))}
      </div>
    </>
  );
}
