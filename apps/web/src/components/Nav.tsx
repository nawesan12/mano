'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';

const items = [
  { href: '/', label: 'Inicio' },
  { href: '/misiones', label: 'Misiones' },
  { href: '/llamadas', label: 'Llamadas' },
  { href: '/clientes', label: 'Clientes' },
  { href: '/cobros', label: 'Cobros' },
  { href: '/reglas', label: 'Reglas' },
  { href: '/memoria', label: 'Memoria' },
  { href: '/diagnostico', label: 'Diagnóstico' },
  { href: '/registro', label: 'Registro' },
  { href: '/conexiones', label: 'Conexiones' },
];

export default function Nav({ pending }: { pending: number }) {
  const path = usePathname();
  return (
    <nav className="nav" aria-label="Secciones">
      {items.map((it) => {
        const current = it.href === '/' ? path === '/' : path.startsWith(it.href);
        return (
          <Link key={it.href} href={it.href} className="navbtn" aria-current={current ? 'page' : undefined}>
            {it.label}
            {it.href === '/' && pending > 0 && <span className="badge">{pending}</span>}
          </Link>
        );
      })}
    </nav>
  );
}
