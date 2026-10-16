import { db, tenantId } from '@/lib/db';
import { ars, when } from '@/lib/format';
import { addCustomerNote } from '../../actions';

type Customer = {
  id: string; name: string | null; phone: string | null; email: string | null; contacts: number;
  paid_total: number; last_contact_at: string | null; last_channel: string | null; notes: string | null;
};

const channel: Record<string, string> = { whatsapp: 'WhatsApp', gmail: 'Mail', mercadopago: 'Mercado Pago' };

export default async function Clientes({ searchParams }: { searchParams: Promise<{ q?: string }> }) {
  const { q = '' } = await searchParams;
  let query = db().from('customers').select('id,name,phone,email,contacts,paid_total,last_contact_at,last_channel,notes')
    .eq('tenant_id', tenantId()).order('last_contact_at', { ascending: false, nullsFirst: false }).limit(200);
  const term = q.trim().replace(/[%,()]/g, '');
  if (term) query = query.or(`name.ilike.%${term}%,email.ilike.%${term}%,phone.ilike.%${term}%`);
  const { data } = await query;
  const rows = (data ?? []) as Customer[];
  return (
    <>
      <h1>Clientes</h1>
      <p className="sub">Se arman solos con cada mensaje, mail y pago. Las notas las usa Mano para responder mejor.</p>
      <form className="addfact" role="search">
        <input name="q" defaultValue={q} placeholder="Buscar por nombre, teléfono o mail" aria-label="Buscar cliente" />
        <button className="btn">Buscar</button>
      </form>
      {rows.length === 0 ? <p className="empty">{term ? 'Nadie coincide con esa búsqueda.' : 'Todavía no hay clientes. Aparecen cuando te escriben o te pagan.'}</p> : (
        <div className="list">
          {rows.map((c) => (
            <details key={c.id} className="card customer">
              <summary>
                <b>{c.name || (c.phone ? '+' + c.phone : c.email)}</b>
                <span className="meta">
                  {c.contacts} contacto{c.contacts === 1 ? '' : 's'}
                  {c.last_contact_at && ` · ${when(c.last_contact_at)} por ${channel[c.last_channel ?? ''] ?? c.last_channel}`}
                  {Number(c.paid_total) > 0 && ` · pagó ${ars(Number(c.paid_total))}`}
                </span>
              </summary>
              <p className="contact">{[c.phone && '+' + c.phone, c.email].filter(Boolean).join(' · ')}</p>
              {c.notes && <pre>{c.notes}</pre>}
              <form action={addCustomerNote} className="addfact">
                <input type="hidden" name="id" value={c.id} />
                <input name="note" placeholder="Agregar una nota (ej. paga a 60 días)" maxLength={300} required aria-label="Nota" />
                <button className="btn">Anotar</button>
              </form>
            </details>
          ))}
        </div>
      )}
    </>
  );
}
