'use client';

import { useActionState } from 'react';
import { login } from '../actions';

export default function Login() {
  const [error, action, pending] = useActionState(login, null);
  return (
    <main className="login">
      <h1>
        <em>Mano</em>
      </h1>
      <p className="sub">Tu mano derecha que nunca duerme.</p>
      <form action={action} className="card">
        <label htmlFor="password">Contraseña del panel</label>
        <input id="password" name="password" type="password" autoComplete="current-password" required autoFocus />
        {error && <p className="err" role="alert">{error}</p>}
        <button className="btn primary" disabled={pending}>{pending ? 'Entrando…' : 'Entrar'}</button>
      </form>
    </main>
  );
}
