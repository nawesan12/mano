import { NextResponse, type NextRequest } from 'next/server';
import { COOKIE, verify } from '@/lib/session';

// Todo el panel pide sesión, salvo el login. Sin PANEL_PASSWORD el panel no abre: mejor cerrado que expuesto.
export async function middleware(req: NextRequest) {
  if (req.nextUrl.pathname.startsWith('/login')) return NextResponse.next();
  if (await verify(process.env.PANEL_PASSWORD, req.cookies.get(COOKIE)?.value)) return NextResponse.next();
  const url = req.nextUrl.clone();
  url.pathname = '/login';
  url.search = '';
  return NextResponse.redirect(url);
}

export const config = { matcher: ['/((?!_next/|favicon.ico).*)'] };
