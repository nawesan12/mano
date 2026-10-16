export const usd = (n: number) => `USD ${n.toFixed(n > 0 && n < 0.1 ? 3 : 2)}`;
export const ars = (n: number) => '$' + Math.round(n).toLocaleString('es-AR');
const tz = 'America/Argentina/Buenos_Aires';
export const when = (s: string) =>
  new Date(s).toLocaleString('es-AR', { day: 'numeric', month: 'numeric', hour: '2-digit', minute: '2-digit', timeZone: tz });
