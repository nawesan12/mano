import { db, tenantId } from '@/lib/db';
import { ars, when } from '@/lib/format';

type Charge = { id: string; name: string | null; description: string; amount: number; status: string; url: string; created_at: string; paid_at: string | null };
type Invoice = { id: string; label: string; customer: string | null; concept: string | null; total: number; cae: string; qr_url: string | null; created_at: string };
type Payout = { id: string; destination: string; amount: number; reason: string; status: string; final: boolean; created_at: string };

const chargeStatus: Record<string, string> = { pending: 'Sin pagar', paid: 'Pagado', expired: 'Vencido' };

export default async function Cobros() {
  const sb = db(), t = tenantId();
  const [charges, payouts, invoices] = await Promise.all([
    sb.from('mp_charges').select('id,name,description,amount,status,url,created_at,paid_at').eq('tenant_id', t).order('created_at', { ascending: false }).limit(50),
    sb.from('mp_payouts').select('id,destination,amount,reason,status,final,created_at').eq('tenant_id', t).order('created_at', { ascending: false }).limit(50),
    sb.from('invoices').select('id,label,customer,concept,total,cae,qr_url,created_at').eq('tenant_id', t).order('created_at', { ascending: false }).limit(50),
  ]);
  const inv = (invoices.data ?? []) as Invoice[];
  const cs = (charges.data ?? []) as Charge[];
  const ps = (payouts.data ?? []) as Payout[];
  const pending = cs.filter((c) => c.status === 'pending').reduce((s, c) => s + Number(c.amount), 0);
  const paid = cs.filter((c) => c.status === 'paid').reduce((s, c) => s + Number(c.amount), 0);
  return (
    <>
      <h1>Cobros</h1>
      <p className="sub">Links de Mercado Pago, facturas de ARCA y transferencias aprobadas.</p>
      <section className="stats">
        <div className="stat"><small>Cobrado por links</small><b>{ars(paid)}</b></div>
        <div className="stat"><small>Sin pagar</small><b>{ars(pending)}</b></div>
        <div className="stat"><small>Transferencias</small><b>{ps.length}</b></div>
      </section>
      <h2 className="gap">Links de pago</h2>
      {cs.length === 0 ? <p className="empty">Todavía no mandaste links. Probá con /cobrar Pérez 150000 factura 123.</p> : (
        <div className="tablewrap"><table>
          <thead><tr><th>Fecha</th><th>Cliente</th><th>Concepto</th><th className="r">Monto</th><th>Estado</th></tr></thead>
          <tbody>{cs.map((c) => (
            <tr key={c.id}>
              <td>{when(c.created_at)}</td><td>{c.name}</td><td><a href={c.url} target="_blank" rel="noreferrer">{c.description}</a></td>
              <td className="r num">{ars(Number(c.amount))}</td><td><span className={`chip ${c.status}`}>{chargeStatus[c.status] ?? c.status}</span></td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      <h2 className="gap">Facturas</h2>
      {inv.length === 0 ? <p className="empty">Sin facturas. Cuando entra un pago, Mano te la deja lista para aprobar.</p> : (
        <div className="tablewrap"><table>
          <thead><tr><th>Fecha</th><th>Comprobante</th><th>Cliente</th><th className="r">Total</th><th>CAE</th></tr></thead>
          <tbody>{inv.map((f) => (
            <tr key={f.id}>
              <td>{when(f.created_at)}</td><td>{f.qr_url ? <a href={f.qr_url} target="_blank" rel="noreferrer">{f.label}</a> : f.label}</td>
              <td>{f.customer}{f.concept && <small className="note">{f.concept}</small>}</td>
              <td className="r num">{ars(Number(f.total))}</td><td className="num">{f.cae}</td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      <h2 className="gap">Transferencias</h2>
      {ps.length === 0 ? <p className="empty">Sin transferencias.</p> : (
        <div className="tablewrap"><table>
          <thead><tr><th>Fecha</th><th>Destino</th><th>Motivo</th><th className="r">Monto</th><th>Estado</th></tr></thead>
          <tbody>{ps.map((p) => (
            <tr key={p.id}>
              <td>{when(p.created_at)}</td><td>{p.destination}</td><td>{p.reason}</td>
              <td className="r num">{ars(Number(p.amount))}</td><td><span className="chip">{p.final ? p.status : 'en proceso'}</span></td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
    </>
  );
}
