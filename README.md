# Mano

Tu mano derecha que nunca duerme. **Fase 0: MVP personal.** Un agente que vigila tu Gmail y tu WhatsApp, te avisa por Telegram, redacta respuestas para que las apruebes con un toque y te manda el resumen cada mañana, sin quemar tokens. **Cuando algo es crítico, te llama**: te cuenta qué pasó y lo resolvés hablando.

## Stack

| Pieza | Tecnología |
| --- | --- |
| Worker (agente, bot, webhooks) | **Go 1.24**, solo librería estándar: un binario sin dependencias |
| Panel | Next.js 15 + React 19 |
| Datos | Supabase (Postgres + pgvector + RLS); el worker usa la API REST |
| IA | Anthropic o cualquier API compatible con OpenAI (DeepSeek, OpenRouter, Ollama). BYOK listo |
| Canales | Telegram (Bot API), WhatsApp Cloud API, Gmail (API REST + Pub/Sub) |
| Voz | WhatsApp Calling API (WebRTC) + voz en tiempo real (OpenAI Realtime). El worker hace de puente con [pion/webrtc](https://github.com/pion/webrtc), la única dependencia externa |

## Cómo piensa

Cada evento recorre una cascada; cada nivel solo pasa lo que no pudo resolver:

1. **Webhooks**, nunca polling (Gmail por Pub/Sub; Telegram por long polling).
2. **Reglas** sin IA, costo cero: newsletters, cambios de CBU, intimaciones.
3. **Clasificador barato** (DeepSeek Flash, Haiku…) decide urgencia e intención.
4. **Modelo top** solo para actuar: redactar, decidir.
5. **Gobernador de gasto**: al 80% del tope pasa a modelo barato, al 100% solo lo crítico, y frena si detecta un loop.

Todo lo que hace queda en `actions_log` (solo inserción), con modelo y costo.

## Estructura

```
apps/worker/            Go
  cmd/mano              entrada: webhooks, bot, resumen diario, watch de Gmail
  internal/core         reglas, cascada, gobernador, clientes de IA (puro, con tests)
  internal/agent        agente: triage → borrador → aprobación → ejecución; resumen
  internal/store        Supabase vía PostgREST
  internal/gmail        Gmail REST + OAuth con refresh token
  internal/owner        comandos y atención al dueño, igual para Telegram y WhatsApp
  internal/telegram     Bot API y tarjetas de aprobación
  internal/whatsapp     Cloud API: webhook firmado, botones, plantillas, ventana de 24 h, costos, llamadas
  internal/voice        Mano te llama: puente WebRTC, sesión de voz, herramientas, reintentos
  internal/stt          transcripción de audios (API compatible con OpenAI)
  internal/tts          notas de voz para lo importante (API compatible con OpenAI)
  internal/server       webhooks de Gmail y otros, /health
  internal/mercadopago  pagos: webhook firmado → evento
  internal/mercadolibre ventas y envíos de Mercado Libre (OAuth con refresh rotativo)
  internal/calendar     Google Calendar: push, sync incremental, movidas, cancelaciones y choques
  internal/schedule     tareas diarias por hora local
apps/web                panel Next.js (estado, pendientes, registro, gasto)
supabase                migración con RLS + seed
```

## Puesta en marcha

1. Supabase: creá el proyecto y corré `supabase/migrations/0001_core.sql` y `supabase/seed.sql`.
2. Telegram: creá el bot con @BotFather.
3. `cp .env.example .env` y completá. **Importante:** `TOP_PRICE_IN` y `TOP_PRICE_OUT` con la tarifa vigente del proveedor; sin eso el worker no arranca.
4. `cd apps/worker && make run`. Mandale `/start` al bot, copiá tu chat id a `TELEGRAM_OWNER_CHAT_ID` y reiniciá.
5. Gmail (opcional para arrancar):
   - En Google Cloud: habilitá Gmail API y Pub/Sub, creá un OAuth client y sacá el refresh token del dueño (scopes `gmail.readonly` y `gmail.send`; sumá `calendar.readonly` si vas a usar Calendar).
   - Creá un topic, dale permiso de publicación a `gmail-api-push@system.gserviceaccount.com` y una suscripción push a `https://TU-WORKER/webhooks/gmail?token=GMAIL_PUSH_TOKEN`.
   - El worker registra y renueva el watch solo; para probarlo a mano: `make watch`.
6. WhatsApp (opcional): ver la sección de abajo.
7. Llamadas (opcional): ver "Mano te llama".
8. Panel: `pnpm install && pnpm dev:web` → http://localhost:3000

## WhatsApp

Mano usa WhatsApp para dos cosas: hablar con vos (si `OWNER_CHANNEL=whatsapp`) y recibir mensajes de tus clientes.

- **Clientes:** cada mensaje entra a la cascada como evento. Si importa, te avisa; si hay que responder, redacta y te deja la tarjeta para aprobar. En la Fase 0 **nunca le contesta solo a un cliente**.
- **Vos:** escribís igual que en Telegram (texto, audios, `/resumen`, `/pendientes`…) y aprobás con botones.
- **Ventana de 24 h:** WhatsApp solo deja escribir libre a quien te escribió en las últimas 24 h. Para avisarte fuera de esa ventana, Mano usa una plantilla; si no hay plantilla, el aviso sale por Telegram. A un cliente con la ventana cerrada no le responde.
- **Costo:** desde el 1-oct-2026 Meta cobra cada mensaje saliente, también los de servicio. Por eso cada turno es un solo mensaje y cada envío queda en `wa_messages` con la categoría y el cargo que informa Meta. Para avisos internos Telegram es gratis.

Puesta en marcha:

1. En Meta for Developers: app de tipo Business con WhatsApp, número de producción y token permanente de usuario de sistema.
2. Webhook: URL `https://TU-WORKER/webhooks/whatsapp`, token de verificación = `WA_VERIFY_TOKEN`, suscribirse a `messages`.
3. Plantilla de aviso (categoría **Utility**, idioma `es_AR`), nombre `mano_aviso`, cuerpo:
   `Aviso de Mano: {{1}}. Respondé este mensaje para ver el detalle.`
   La variable no puede ir ni al principio ni al final del cuerpo. Mantenela seca y transaccional: si suena promocional, Meta la recategoriza como marketing y cuesta más.
4. Correr `supabase/migrations/0002_whatsapp.sql`.

## Google Calendar

Mano vigila tu calendario con notificaciones push de Google y sincronización incremental, sin polling. Avisa sin IA de lo que hicieron **otros**:

| Cambio | Urgencia |
| --- | --- |
| Te cancelaron una reunión | Alta (nota de voz) |
| Te movieron una reunión | Alta |
| Algo nuevo o movido se pisa con otra cosa | Alta |
| Invitación nueva | Al resumen |

Lo que movés vos no te lo avisa. Mira solo las próximas dos semanas. El resumen de la mañana trae la agenda del día.

Puesta en marcha: habilitá Google Calendar API en el mismo proyecto de Gmail y sacá el refresh token con los scopes de Gmail más `calendar.readonly`. En `.env`: `CALENDAR_ENABLED=true`, `PUBLIC_URL` (https con certificado válido) y `CALENDAR_PUSH_TOKEN`. Correr `0007_calendar.sql`. La primera sincronización solo llena la copia local; el canal push se renueva a diario.

## Mercado Pago

Cada pago que se resuelve entra a la cascada como un evento, igual que un mail. Las reglas de arranque (`0006_mercadopago.sql`) lo clasifican sin IA:

| Pago | Urgencia | Qué pasa |
| --- | --- | --- |
| Contracargo | Crítica | Te llama |
| Rechazado | Alta | Nota de voz |
| Acreditado de $500.000 o más | Alta | Nota de voz |
| Acreditado | Normal | Al resumen |

Se cambia con `/regla`, por ejemplo: `/regla llamame si entra un pago de más de 2.000.000`.

### Cobrar, devolver y ver movimientos

- **`/cobrar Pérez 150000 factura 123`:** te muestra el mensaje que le va a mandar y, **solo si lo aprobás**, arma el link de pago (vence en 7 días) y se lo manda a Pérez por WhatsApp. Si su ventana de 24 h está cerrada, te pasa el link para que lo reenvíes vos.
  - Cuando paga, el webhook marca el cobro y te avisa como importante: "te pagaron un link de cobro".
  - Si a los 3 días no pagó, te lo recuerda una vez y te sugiere `/llamar Pérez: recordarle el pago`. Si vence, te avisa.
- **`/devolver 1234567890 [monto]`:** devolución total o parcial. Es **riesgo alto** y se aprueba **solo con el botón, nunca por voz**. Lleva clave de idempotencia: un doble toque no devuelve dos veces.
- **`/cobros`:** lo cobrado en las últimas 24 h y en 7 días (bruto, neto, por liberar, devuelto), los últimos pagos y los links sin pagar. Mercado Pago no tiene un endpoint oficial de saldo; esto sale de la búsqueda de pagos.

Correr `0009_mp_cobros.sql`.

### Transferencias (apagadas por defecto)

`/transferir proveedor@mail.com 50000 factura 88` o `/transferir <CBU/CVU> 50000 factura 88 titular Juan Pérez dni 12345678`.

- **Riesgo alto.** Se aprueba **solo con el botón de la tarjeta**, nunca por voz.
- **Topes duros** por operación y por día (`MP_PAYOUT_MAX_ARS`, `MP_PAYOUT_DAY_MAX_ARS`). Se vuelven a revisar al ejecutar.
- **Destino:** el CBU se valida con sus dígitos verificadores, así que un número mal tipeado no pasa. Para CBU hacen falta el titular y el DNI. Los alias no están soportados por la API.
- **Resultado:** es asíncrono. Mano te avisa cuando se acredita o si falla.
- **Seguridad:** cada pedido va firmado con Ed25519 y lleva idempotencia (`mano-payout-<aprobación>`). El aviso de Mercado Pago no viene firmado, así que solo dispara una consulta a la API.

Para activarlas:
1. Generá la clave con `openssl genpkey -algorithm ed25519 -out mp_payouts.pem` y sacá la pública con `openssl pkey -in mp_payouts.pem -pubout`.
2. Pedile a Mercado Pago (equipo de Integraciones) que habilite Payouts para tu cuenta y registre la clave pública. Sin eso la API responde 403 y Mano te lo dice.
3. Configurá `MP_PAYOUTS_ENABLED=true` y `MP_PAYOUTS_KEY_FILE`, y corré `0010_mp_transferencias.sql`.
4. Para probar antes de producción: credenciales de prueba con `MP_PAYOUTS_TEST=true`. Si configurás `PUBLIC_URL`, cada link avisa directo a `/webhooks/mercadopago`.

Puesta en marcha: en Mercado Pago, Tus integraciones → tu app → Webhooks. URL `https://TU-WORKER/webhooks/mercadopago`, evento **Pagos**. Copiá la clave secreta a `MP_WEBHOOK_SECRET` y el access token de producción a `MP_ACCESS_TOKEN`. La firma `x-signature` se valida siempre y los pagos se consultan a la API, nunca se confía en el cuerpo del webhook.

## Mercado Libre: ventas y envíos

La API de Mercado Pago no tiene envíos para Argentina, así que Mano los sigue desde **Mercado Libre** (Mercado Envíos).

| Aviso | Urgencia |
| --- | --- |
| Venta nueva | Alta (nota de voz) |
| Envío listo para despachar ("imprimí la etiqueta") | Alta |
| Envío no entregado o cancelado | Alta |
| Venta cancelada | Alta |
| En camino o entregado | Al resumen |

`/envios` lista lo que está en curso, con el número de seguimiento.

Seguridad: las notificaciones de Mercado Libre no vienen firmadas. Mano solo acepta las de tu app y tu vendedor, y siempre vuelve a consultar el envío o la venta a la API.

Puesta en marcha:
1. Creá una app en developers.mercadolibre.com.ar con los scopes de lectura y `offline_access`.
2. Notificaciones: URL `https://TU-WORKER/webhooks/mercadolibre`, tópicos `orders_v2` y `shipments`.
3. Autorizá la app con tu usuario vendedor y sacá el refresh token.
4. `.env`: `ML_CLIENT_ID`, `ML_CLIENT_SECRET` y `ML_REFRESH_TOKEN`. Corré `0011_mercadolibre.sql`.

El refresh token es de un solo uso: Mano guarda el nuevo en cada renovación. Si se pierde (por ejemplo, borrando `ml_auth`), hay que volver a autorizar la app.

## Cómo te avisa

| Urgencia | Ejemplo | Cómo te llega |
| --- | --- | --- |
| Crítica | Cambio de CBU, intimación, cliente grande enojado | **Llamada** (ver "Mano te llama") más el aviso por escrito |
| Alta | Lead con intención real, reunión movida | **Nota de voz** con el texto abajo, más la tarjeta para aprobar |
| Normal | Info que puede esperar | En el resumen de la mañana |

La nota de voz reemplaza al aviso escrito, no se suma: en WhatsApp sigue siendo un solo mensaje. Necesita `TTS_BASE_URL`; sin eso, el aviso va por texto. Pasa por el gobernador como cualquier IA, y si se frena por gasto también va por texto. Fuera de la ventana de 24 h de WhatsApp sale la plantilla de aviso. Correr `supabase/migrations/0005_notas_de_voz.sql`.

## Seguimiento

Lo que espera tu ok no se pierde. Todo esto se arma con código, sin IA:

- **Recordatorio:** si un pendiente lleva 4 horas sin respuesta, te lo recuerda una sola vez, en un único mensaje con todo lo que se juntó. No te escribe en horario de silencio.
- **Vencimiento:** a las 72 horas el pendiente vence, Mano no hace nada y lo deja anotado en el registro. Tocar la tarjeta después ya no tiene efecto.
- **Cierre del día** (`BRIEF_CLOSE_AT`, por ejemplo `19:00`): qué hizo hoy, qué queda pendiente y cuánto gastó en IA.

## Mano te llama

Cuando entra algo **crítico** (cambio de CBU, intimación, cliente grande enojado) o algo que vos pediste con una regla, Mano te llama por WhatsApp. Al atender te dice "Hola, soy Mano" y el motivo; le contestás hablando: "mandale que lo verificamos", "descartalo", "no me avises más de este", "anotá que Pérez paga a 60 días". Antes de aprobar te lee qué va a mandar y espera tu sí. Al cortar te deja el resumen por mensaje.

- **Qué llama:** urgencia `critical`, o una regla con `"notify":"call"`. Lo `high` sigue llegando por mensaje.
- **Cuándo no:** horario de silencio (21 a 8 por defecto), tope diario (3), o si no atendiste las dos últimas (WhatsApp revoca el permiso a las 4 sin atender). Una regla con `"ignoreQuiet":true` puede llamar de noche.
- **Si no atendés:** un solo reintento a los 10 minutos si sigue pendiente; después, mensaje. Si cortás, no insiste.
- **También al revés:** llamá vos al número de Mano y hablale. Las llamadas entrantes no las cobra Meta. A números que no son el tuyo no los atiende.
- **Costo:** Meta cobra el minuto saliente (Argentina ≈ USD 0,0101) y la voz pasa por el gobernador como cualquier IA: se autoriza el peor caso antes de marcar y se registra el consumo real en `usage` (tier `voice`). Cada llamada queda en `calls` y en `actions_log`.

Cómo funciona: Meta y el modelo de voz esperan los dos una oferta WebRTC, así que el worker abre una conexión con cada uno y reenvía el audio Opus tal cual, sin transcodificar. Las herramientas de la llamada (aprobar, descartar, silenciar, recordar, cortar) pasan por `Agent.OnDecision`, el mismo camino que los botones.

Puesta en marcha:

1. Correr `supabase/migrations/0003_calls.sql`.
2. En Meta: habilitar **Calling** en el número (WhatsApp Manager → número → Llamadas), suscribir el webhook al campo **`calls`** y activar `callback_permission_status` para que llamar una vez a Mano ya te dé el permiso.
3. Permiso: escribile cualquier cosa a Mano por WhatsApp y mandá `/llamame`. Si falta el permiso, te llega el pedido; elegí **Siempre permitir**.
4. `.env`: `VOICE_ENABLED=true`, `REALTIME_API_KEY` y `REALTIME_PRICE_IN/OUT` con la tarifa vigente del modelo.
5. VPS: WebRTC usa UDP. Abrí el rango UDP efímero en el firewall y, con Docker, corré con `--network host`.
6. Probá: `/llamame`, atendé y decí "¿qué tengo pendiente?".

**Llamar a un cliente:** `/llamar Pérez: recordarle que vence la factura 123`. Mano busca a Pérez entre tus contactos de WhatsApp y te muestra la llamada como tarjeta. **Sale solo si la aprobás.** Al cliente le dice que es el asistente virtual del negocio, cumple el objetivo y nada más: no promete, no negocia, no aprueba. Anota lo que le dicen ("paga el viernes") y te lo cuenta al cortar. Sin reintentos y nunca en horario de silencio. Si el cliente no dio permiso de llamada, le manda el pedido (fuera de la ventana de 24 h necesita la plantilla `WA_TEMPLATE_CALL_PERMISSION`). Correr `0008_llamadas_clientes.sql`.

Ajustes por chat: `/silencio 22-7`, `/llamadas 5`, `/llamadas off`, `/regla llamame si escribe Liliana o si entra un pago de más de 500.000`.

## Primera prueba real (checklist)

1. Migraciones `0001` a `0008` corridas en Supabase.
2. En el VPS: `.env` completo y `make check`. Prueba cada integración contra el servicio real: Supabase, Telegram, WhatsApp, permiso de llamada, WebRTC/STUN (IP pública), voz en tiempo real, notas de voz, Google y Mercado Pago. No manda mensajes ni llama. Hasta que no diga "Todo en orden", no sigas.
3. `make run` (o Docker con `--network host`). Mirá `/health`.
4. Escribile a Mano por WhatsApp (abre la ventana) y mandá `/llamame`. Si falta el permiso, aceptá el pedido con **Siempre permitir** y repetí.
5. Atendé y decí "¿qué tengo pendiente?". En el log tienen que aparecer, en orden, la oferta, el `connect` de Meta, `ACCEPTED` y la conexión con el modelo. Si se corta en el `connect`, buscá `voz: sdp de meta`.
6. Mandate un mail con "nuevo CBU" para probar la ruta crítica completa: aviso, borrador y llamada. Aprobá hablando.
7. Llamá vos al número de Mano.
8. Revisá en el panel la llamada, su costo y el registro.

## En el VPS

```
cd apps/worker && make build        # bin/mano, binario estático para Linux
# o con Docker:
docker build -t mano-worker apps/worker
docker run -d --restart=always --env-file .env --network host mano-worker   # host: las llamadas usan UDP
```

## Comandos del bot

| Comando | Qué hace |
| --- | --- |
| texto o audio | Le hablás como a un empleado |
| `/resumen` | El resumen del día, ahora |
| `/pendientes` | Vuelve a mostrar lo que espera tu ok |
| `/gasto` | Gasto de IA del día, mes y última hora |
| `/recordar Tema: dato` | Le enseñás algo del negocio |
| `/olvidar Tema` | Borra lo que sabía de ese tema |
| `/regla …` | Le explicás en tus palabras cuándo avisarte o llamarte; te la muestra para aprobar |
| `/llamame` | Te llama ahora con lo pendiente |
| `/llamar Quién: para qué` | Propone llamar a un cliente; sale solo si la aprobás |
| `/cobrar Quién monto concepto` | Arma un link de Mercado Pago y se lo manda al cliente, si lo aprobás |
| `/cobros` | Lo cobrado en 24 h y 7 días, y los links sin pagar |
| `/devolver pago [monto]` | Devolución; solo con el botón, nunca por voz |
| `/envios` | Envíos de Mercado Libre en curso |
| `/transferir destino monto motivo` | Transferencia; topes duros, solo con el botón (apagada por defecto) |
| `/silencio 21-8` | Horario en que no te llama (sin argumento, muestra la configuración) |
| `/llamadas 3` | Máximo de llamadas por día; `off` / `on` para apagarlas o prenderlas |

Las tarjetas de aprobación traen **Aprobar**, **Descartar** y **No me avises más de este remitente**, que crea una regla: ese remitente deja de gastar IA.

## Verificación

```
cd apps/worker && make test   # go vet + tests con -race (incluye flujo completo mail → aprobación → envío)
pnpm typecheck                # panel
```

## Próximo (Fase 1)

Embeddings en `memory_chunks`, login en el panel con Supabase Auth, cifrado de tokens por tenant, notas de voz para lo importante y recepcionista de voz para clientes.
