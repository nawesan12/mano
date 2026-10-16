type Status = Record<string, boolean>;

const names: Record<string, [string, string]> = {
  telegram: ['Telegram', 'Te habla y aprobás con botones'],
  whatsapp: ['WhatsApp', 'Clientes y canal con vos'],
  llamadas: ['Llamadas', 'Te llama por lo crítico'],
  notas_de_voz: ['Notas de voz', 'Lo importante como audio'],
  transcripcion: ['Audios', 'Entiende tus audios'],
  gmail: ['Gmail', 'Lee y responde tu mail'],
  inbox_limpio: ['Inbox limpio', 'Archiva newsletters y automáticos'],
  calendar: ['Google Calendar', 'Movidas, cancelaciones y choques'],
  mercadopago: ['Mercado Pago', 'Pagos, links de cobro y devoluciones'],
  transferencias: ['Transferencias', 'Mandar plata (requiere habilitación)'],
  mercadolibre: ['Mercado Libre', 'Ventas y envíos'],
  arca: ['ARCA', 'Facturas y notas de crédito'],
  memoria: ['Memoria con búsqueda', '"¿Qué me dijo Pérez?"'],
  respaldo_ia: ['IA de respaldo', 'Otro modelo si el principal falla'],
};

async function status(): Promise<Status | null> {
  const url = process.env.WORKER_URL, token = process.env.PANEL_TOKEN;
  if (!url || !token) return null;
  try {
    const res = await fetch(`${url.replace(/\/$/, '')}/api/status`, { headers: { Authorization: `Bearer ${token}` }, cache: 'no-store' });
    return res.ok ? ((await res.json()) as Status) : null;
  } catch {
    return null;
  }
}

export default async function Conexiones() {
  const s = await status();
  return (
    <>
      <h1>Conexiones</h1>
      <p className="sub">Qué tiene conectado Mano. Se configura en el .env del worker; el README explica cada una.</p>
      {!s ? <p className="empty">No pude hablar con el worker. Revisá WORKER_URL y PANEL_TOKEN, y que esté corriendo.</p> : (
        <div className="tablewrap"><table>
          <thead><tr><th>Conexión</th><th>Para qué</th><th>Estado</th></tr></thead>
          <tbody>{Object.entries(names).map(([k, [name, what]]) => (
            <tr key={k}>
              <td>{name}</td><td>{what}</td>
              <td><span className={`chip ${s[k] ? 'on' : 'off'}`}>{s[k] ? 'Activa' : 'Sin conectar'}</span></td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
    </>
  );
}
