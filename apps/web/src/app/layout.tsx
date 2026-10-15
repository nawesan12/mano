import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = { title: 'Mano', description: 'Tu mano derecha que nunca duerme' };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="es">
      <body>{children}</body>
    </html>
  );
}
