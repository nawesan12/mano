import Nav from '@/components/Nav';
import { db, tenantId } from '@/lib/db';
import { logout } from '../actions';

export const dynamic = 'force-dynamic';

export default async function PanelLayout({ children }: { children: React.ReactNode }) {
  const { count } = await db().from('approvals').select('id', { count: 'exact', head: true }).eq('tenant_id', tenantId()).eq('status', 'pending');
  return (
    <div className="app">
      <aside className="rail">
        <div className="brand">Mano</div>
        <Nav pending={count ?? 0} />
        <form action={logout} className="out">
          <button className="linkbtn">Salir</button>
        </form>
      </aside>
      <main className="main">{children}</main>
    </div>
  );
}
