# Mano

Tu mano derecha que nunca duerme. **Fase 0: MVP personal.** Un agente que vigila tu Gmail y tu WhatsApp, te avisa por Telegram, redacta respuestas para que las apruebes con un toque y te manda el resumen cada mañana, sin quemar tokens. **Cuando algo es crítico, te llama**: te cuenta qué pasó y lo resolvés hablando.

## Stack

| Pieza | Tecnología |
| --- | --- |
| Worker (agente, bot, webhooks) | **Go 1.24**, solo librería estándar: un binario sin dependencias |
| Panel | Next.js 15 + React 19 |
| Datos | Supabase (Postgres + pgvector + RLS); el worker usa la API REST |
| IA | Claude, OpenAI, Gemini, DeepSeek, OpenRouter, Groq, Mistral, xAI, Together, Fireworks, Cerebras, Ollama o cualquier API compatible con OpenAI. Con el nombre del proveedor alcanza; respaldo automático si uno falla. BYOK listo |
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
  internal/arca         facturación electrónica: WSAA (CMS) + WSFEv1
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
   - En Google Cloud: habilitá Gmail API y Pub/Sub, creá un OAuth client y sacá el refresh token del dueño (scopes `gmail.readonly` y `gmail.send`; sumá `gmail.modify` para el inbox limpio y `calendar.readonly` para Calendar).
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

## Facturación con ARCA

- **Al cobrar:** cuando se acredita un pago de Mercado Pago, te queda la factura lista para aprobar ("Facturar $150.000 a Juan Pérez").
- **A mano:** `/facturar 150000 Juan Pérez: diseño de logo`, o "haceme la factura de 150 lucas a Pérez".
- **Aprobar:** solo con el botón o desde el panel, nunca por voz, porque queda informada en ARCA y solo se anula con nota de crédito.
- **Al emitir:** te devuelve el número, el CAE y el link del QR para verificarla. El mismo pago no se factura dos veces.

**Notas de crédito:** si devolvés (`/devolver`) un pago que estaba facturado, Mano te deja lista la nota de crédito C asociada por el monto devuelto. También a mano, con `/anular 124 [monto]`. Nunca anula más de lo que queda facturado, y se aprueban solo con el botón. Correr `0018_notas_credito.sql`.

**Fase 0:** Factura C (monotributo) a consumidor final. Desde `ARCA_CF_MAX` pide el DNI (`/facturar 12000000 Juan dni 12345678`). Factura A/B con IVA y el PDF con el QR quedan para más adelante; el link del QR es el oficial y sirve para verificarla.

Cómo funciona: firma el pedido de acceso con el certificado de tu CUIT (CMS armado con la librería estándar y verificado con openssl en los tests), lo manda a WSAA y pide el CAE a WSFEv1 con el próximo número. El ticket de acceso (unas 12 h) queda guardado, porque ARCA no da otro mientras siga vigente.

Puesta en marcha (empezá en homologación):
1. Generá la clave y el pedido de certificado: `openssl genrsa -out arca.key 2048` y `openssl req -new -key arca.key -subj "/C=AR/O=Tu Negocio/CN=mano/serialNumber=CUIT 20111111112" -out arca.csr`.
2. **Homologación:** en ARCA, WSASS → creá el certificado con el CSR y asociá el servicio `wsfe`. **Producción:** Administración de Certificados Digitales, y después Administrador de Relaciones → **delegá el servicio "Facturación electrónica" a ese certificado**. Nunca se comparte la clave fiscal.
3. Dá de alta un **punto de venta de tipo Web Services**.
4. `.env`: `ARCA_CUIT`, `ARCA_ENV`, `ARCA_CERT_FILE`, `ARCA_KEY_FILE`, `ARCA_PTO_VTA`. Corré `0016_arca.sql` y `make check`: tiene que mostrar el último comprobante.

## Clientes

Los clientes se arman solos, sin cargar nada a mano: cada mensaje de WhatsApp, cada mail que no es automático y cada pago suma contacto, último canal y total pagado. Se identifican por teléfono o mail; lo ignorado (newsletters) no cuenta.

- **`/cliente Pérez`** ("¿qué sabés de Pérez?"): ficha con contactos, último contacto, total pagado, links sin pagar y notas.
- **`/nota Pérez: paga a 60 días`** ("anotá que Pérez…"): suma una nota con fecha.
- **Al redactar** una respuesta a ese cliente, Mano usa la ficha y las notas como contexto.
- `/cobrar` y `/llamar` también encuentran a los clientes que dejaron su teléfono.
- En el panel, **Clientes** permite buscar y agregar notas.

Correr `0015_clientes.sql`.

## Diagnóstico semanal

Los lunes a las 9, o cuando lo pidas con `/diagnostico` ("¿cuánto me ahorraste?"), Mano te manda cómo fue la semana:

- cuántas cosas entraron y qué porcentaje resolvió sin IA;
- qué hizo: respuestas, llamadas, cobros y cuentas sospechosas frenadas;
- cuánto tiempo te ahorró y cuánto costó (IA y llamadas).

Sale de los datos, sin IA. El ahorro usa minutos por tarea a la vista y conservadores, en `internal/agent/report.go`: 1 min por mensaje clasificado, 6 por respuesta, 3 por llamada, 5 por link de cobro, 10 por fraude frenado. Cada semana queda guardada y el panel la muestra en **Diagnóstico**. Correr `0014_diagnostico.sql`.

## Modo ausencia

`/ausente hasta el lunes`, o en tus palabras: "me voy de viaje hasta el 20". Fechas que entiende: "el lunes", "20/10", "3 días", "mañana", hasta 60 días.

- **Llamadas:** solo te llama por lo crítico. Lo importante no te avisa al momento y espera al resumen, que arranca con "Mientras no estás, esto pasó".
- **Recordatorios:** no te manda recordatorios de pendientes.
- **Clientes:** si aprobás el mensaje de ausencia (sale como tarjeta y lo podés dictar: `/ausente 20/10: estoy de viaje, te respondo el lunes`), a cada cliente que escriba por WhatsApp se lo manda **una sola vez**. Sin aprobar, no le escribe a nadie.
- **Al volver:** con `/volvi`, o sola cuando vence, te manda un resumen de cuántas cosas entraron por urgencia, a cuántos clientes les respondió y qué te espera.

Correr `0013_ausencia.sql`.

## Antifraude: cuentas bancarias

La estafa más común a PyMEs es el mail del "proveedor" con un CBU nuevo. Mano recuerda qué CBU/CVU o alias usó cada remitente (por mail o teléfono, nunca por el nombre, que se falsifica fácil):

- **La primera vez** queda guardada como de confianza.
- **Si después aparece una distinta**, el mensaje pasa a **crítico** aunque el texto no diga "nuevo CBU": te llama y te muestra la cuenta de antes y la nueva ("CBU …0125; antes CBU …5201").
- **Para que la nueva pase a ser de confianza** hay una tarjeta aparte, de riesgo alto, que se aprueba **solo con el botón**. Aprobala solo si lo confirmaste llamando al número de siempre.

Los CBU se reconocen por sus dígitos verificadores, así que un número de factura no se confunde. Todo sin IA. Correr `0012_antifraude.sql`.

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

## Inbox limpio

Con `GMAIL_ARCHIVE_IGNORED=true`, lo que Mano ignora (newsletters, `noreply`, avisos automáticos y remitentes que silenciaste) sale del inbox con la etiqueta **Mano/Archivado**.

- **No borra nada:** es reversible, el nivel 2 de autonomía ("hace lo reversible y avisa").
- **Te avisa:** el resumen de la mañana dice cuántos archivó.
- **Deshacer:** `/desarchivar` devuelve lo de la última semana, y `/desarchivar tienda.com` solo lo de ese remitente.

Requiere el scope `gmail.modify` en el refresh token, y que la misión de **inbox** esté en nivel 2 (**Ejecuta y avisa**) desde el panel → Misiones. En "Observa" o "Prepara" no archiva aunque esté habilitado. Correr `0020_inbox_limpio.sql`.

## Memoria con búsqueda

"¿Qué me dijo Pérez del contrato?", "¿cuándo vence lo de Gómez?" o `/buscar …`: Mano busca en lo que te llegó y te contesta citando de quién y de qué fecha.

- **Con embeddings** (`EMBED_*`, por ejemplo OpenAI `text-embedding-3-small`): cada mail o mensaje que no se ignoró se guarda con su vector en `memory_chunks` (pgvector) y se busca por significado. Funciona aunque no uses las mismas palabras.
- **Sin embeddings:** busca por palabras en los eventos.
- Lo encontrado va al modelo como **dato, no instrucciones**. Si el gobernador frena la IA, te muestra los fragmentos tal cual.
- Los embeddings pasan por el gobernador y quedan en `usage`.

Correr `0019_memoria.sql`.

## Modelos

Cada nivel (barato para clasificar y resumir, top para redactar y decidir) puede ser de un proveedor distinto: `CHEAP_PROVIDER=gemini`, `TOP_PROVIDER=anthropic`. Con el nombre alcanza, la URL se completa sola. Para un proveedor que no está en la lista, usá `openai-compatible` con su `BASE_URL`.

**Respaldo:** con `FALLBACK_*` configurado, si el proveedor de un nivel está caído, saturado, sin saldo o rechaza la key, Mano reintenta con ese modelo. Un pedido mal armado (400) no se reintenta. El costo se registra según el modelo que respondió, así que el gobernador mide lo real. `make check` le manda un pedido mínimo a cada modelo para validar key, URL y nombre.

Los precios (`*_PRICE_IN/OUT`) siempre se cargan a mano con la tarifa vigente: sin eso el gobernador no puede controlar el gasto.

## Panel

`pnpm dev:web` → http://localhost:3000. Pide la contraseña `PANEL_PASSWORD` y guarda una cookie firmada por 30 días; si cambiás la contraseña, se cierran todas las sesiones.

- **Inicio:** estado del día, tarjetas con **Aprobar, Descartar y No me avises más**, llamadas y registro. Las decisiones van al worker (`POST /api/decide`, con `PANEL_TOKEN`) y se ejecutan en `Agent.OnDecision`, el mismo camino que los botones del chat. Desde el panel también se aprueba lo que mueve plata: es un clic explícito.
- **Llamadas:** cada llamada con resumen, transcripción y costo.
- **Cobros:** links de pago y transferencias.
- **Reglas:** cada regla en una frase, con sus usos; se pueden pausar.
- **Memoria:** agregar u olvidar datos del negocio.
- **Registro:** todo lo que hizo Mano, quién lo aprobó y cuánto costó.
- **Misiones:** el nivel de autonomía de cada una (Observa, Prepara, Ejecuta y avisa, Piloto automático), pausarlas y agregar nuevas. El nivel manda: por ejemplo, el inbox limpio solo archiva en nivel 2 o más. Lo que mueve plata o habla por vos siempre pide tu ok, en cualquier nivel.
- **Conexiones:** qué integraciones tiene activas el worker (`GET /api/status`, con `PANEL_TOKEN`).

Supabase Auth con varios usuarios queda para la Fase 1.

## Rubros (playbooks)

Al mandar `/start`, Mano pregunta qué negocio es. `/negocio Costa Azul, inmobiliaria` (o "tengo una inmobiliaria que se llama Costa Azul") carga lo típico de ese rubro:

| Rubro | Reconoce sin IA, por ejemplo |
| --- | --- |
| Inmobiliaria | Seña o boleto, pedido de tasación (lead), visita, consulta de portal, reclamo de inquilino |
| Consultorio | Posible urgencia (crítica: derivar), turno, cancelación, obra social |
| Gastronomía | Queja de salud (crítica), reserva, pedido o delivery, proveedor |
| E-commerce | Desconocimiento de compra (crítica), reclamo de envío, cambio o devolución, mayorista |
| Servicios profesionales | Vencimiento o intimación (crítica), presupuesto, reunión, pagos |

También suma dos misiones del rubro y te sugiere qué contarle (`/recordar Honorarios: …`). Si cambiás de rubro, se sacan las reglas del anterior; las tuyas no se tocan. Están en `internal/core/playbooks.go`. Correr `0017_playbooks.sql`.

## Hablale normal

No hace falta saberse los comandos: "cobrale 150 lucas a Pérez por la factura 123", "llamalo a Gómez para recordarle que vence hoy", "acordate que los honorarios son 3% más IVA", "¿cómo vienen los cobros?". Funciona igual con audios.

Mano lo traduce al comando equivalente y lo corre con las mismas validaciones. Lo que mueve plata o habla con un cliente sigue saliendo como **tarjeta para aprobar**. El clasificador barato solo se usa si el texto suena a un pedido; la charla normal no lo gasta. La respuesta arranca con lo que entendió ("Entendí: /cobrar Pérez 150000 factura 123"), en el mismo mensaje.

## Comandos del bot

| Comando | Qué hace |
| --- | --- |
| texto o audio | Le hablás como a un empleado |
| `/negocio Nombre, rubro` | Configura el negocio y carga el playbook del rubro |
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
| `/facturar monto cliente: concepto` | Factura C en ARCA; solo con el botón |
| `/desarchivar [remitente]` | Devuelve al inbox lo que Mano archivó |
| `/buscar pregunta` | Busca en lo que te llegó y responde citando quién y cuándo |
| `/anular número [monto]` | Nota de crédito C que anula una factura |
| `/cliente Quién` · `/nota Quién: dato` | Ficha del cliente y notas que Mano usa al responder |
| `/ausente hasta el lunes` · `/volvi` | Modo ausencia: solo lo crítico, respuesta a clientes aprobada |
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
