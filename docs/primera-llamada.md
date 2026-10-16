# Primera llamada real

Objetivo: que Mano te llame por WhatsApp y lo atiendas. Con WhatsApp y la voz alcanza; Gmail, Mercado Pago y lo demás se suman después.

## 1. Lo que necesitás

- Un VPS con IP pública (Ubuntu o Debian) y un dominio o subdominio con un registro A apuntando a esa IP (ej. `mano.tudominio.com`).
- Un proyecto de Supabase.
- Una app de Meta for Developers de tipo Business con WhatsApp, un **número de producción** (no el de prueba) y un **token permanente** de usuario de sistema.
- Una API key de OpenAI con acceso a la voz en tiempo real.

## 2. Base de datos

```
DATABASE_URL="postgresql://…" SEED=1 ./supabase/aplicar.sh
```

Aplica todas las migraciones en orden, no repite ninguna y con `SEED=1` carga el tenant inicial.

## 3. Configuración

```
cp deploy/primera-llamada.env .env
```

Completá lo vacío. Los precios (`*_PRICE_*`) van con la tarifa vigente del proveedor: sin eso el worker no arranca, porque el gobernador necesita saber cuánto gasta.

## 4. Levantarlo con HTTPS

```
cd deploy && MANO_DOMAIN=mano.tudominio.com docker compose up -d --build
```

Caddy saca el certificado solo. Firewall: abrí **TCP 80 y 443** (HTTPS) y el **UDP de las llamadas** (en Ubuntu: `sudo ufw allow 80,443/tcp` y `sudo ufw allow 10000:65535/udp`). Probá `https://mano.tudominio.com/health`.

Sin Docker: `cd apps/worker && make build`, copiá `bin/mano` y usá `deploy/mano.service`, más Caddy instalado con el `Caddyfile`.

## 5. Chequeo

```
docker compose exec worker /mano -check     # o: cd apps/worker && make check
```

Tiene que decir "Todo en orden". Prueba Supabase, WhatsApp, el modelo, que el VPS tenga IP pública para la voz y que el modelo de voz acepte la conexión. No manda mensajes ni llama.

## 6. Meta

1. **Webhook:** URL `https://mano.tudominio.com/webhooks/whatsapp`, token = `WA_VERIFY_TOKEN`. Suscribí los campos **`messages`** y **`calls`**.
2. **Llamadas:** WhatsApp Manager → tu número → **Llamadas** → activalas. Activá también "callback permission" para que llamar una vez al número de Mano ya cuente como permiso.

## 7. La llamada

1. Desde tu celular (`WA_OWNER_PHONE`), escribile "hola" al número de Mano. Eso abre la ventana de 24 h.
2. Mandá **`/llamame`**. Si falta el permiso, te llega el pedido: elegí **Siempre permitir** y repetí.
3. Atendé y decí "¿qué tengo pendiente?".

En los logs (`docker compose logs -f worker`) tiene que aparecer, en orden: la oferta, el `connect` de Meta, `ACCEPTED` y la conexión con el modelo.

## Si algo falla

| Síntoma | Qué mirar |
| --- | --- |
| `make check` falla en "WebRTC / STUN" | El VPS no sale por UDP o no tiene IP pública. Revisá el firewall del proveedor (no solo ufw). |
| `voz: sdp de meta` en el log | Meta rechazó el audio de la oferta. Pasame el log completo de esa llamada. |
| Suena pero no se escucha nada | UDP entrante bloqueado. Abrí el rango UDP en el firewall del proveedor. |
| "falta el permiso de llamada" | Aceptá el pedido en WhatsApp con "Siempre permitir". |
| No llega nada a `/webhooks/whatsapp` | El certificado o la URL. Probá `/health` desde afuera y revisá la suscripción a `messages` y `calls`. |
