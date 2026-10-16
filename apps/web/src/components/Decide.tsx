'use client';

import { useActionState } from 'react';
import { decide } from '@/app/actions';

// Botones de una tarjeta de aprobación. Ejecuta en el worker; la respuesta se muestra en la misma tarjeta.
export default function Decide({ id, canIgnore }: { id: string; canIgnore: boolean }) {
  const [msg, action, pending] = useActionState(decide, null);
  if (msg) return <p className="done" role="status">{msg}</p>;
  return (
    <form action={action} className="acts">
      <input type="hidden" name="id" value={id} />
      <button className="btn primary" name="decision" value="approve" disabled={pending}>Aprobar</button>
      <button className="btn" name="decision" value="reject" disabled={pending}>Descartar</button>
      {canIgnore && (
        <button className="btn danger" name="decision" value="ignore-sender" disabled={pending}>No me avises más</button>
      )}
    </form>
  );
}
