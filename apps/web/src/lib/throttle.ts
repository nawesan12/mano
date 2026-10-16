// Freno de intentos de login, en memoria del servidor (Fase 0: un solo proceso del panel).
// 5 fallidos por IP cada 15 min y 50 en total: sin esto, la contraseña se puede adivinar probando.
const WINDOW = 15 * 60 * 1000;
const PER_IP = 5;
const GLOBAL = 50;

const fails = new Map<string, number[]>();
let all: number[] = [];

function recent(list: number[], now: number) {
  return list.filter((t) => now - t < WINDOW);
}

export function blocked(ip: string, now = Date.now()): boolean {
  all = recent(all, now);
  const mine = recent(fails.get(ip) ?? [], now);
  fails.set(ip, mine);
  return mine.length >= PER_IP || all.length >= GLOBAL;
}

export function fail(ip: string, now = Date.now()) {
  fails.set(ip, [...recent(fails.get(ip) ?? [], now), now]);
  all = [...recent(all, now), now];
  if (fails.size > 10_000) fails.clear(); // no crecer sin límite
}

export function succeed(ip: string) {
  fails.delete(ip);
}
