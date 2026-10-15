# Mano

Tu mano derecha que nunca duerme. **Fase 0: MVP personal.** Un agente que vigila tu Gmail, te avisa por Telegram, redacta respuestas para que las apruebes con un toque y te manda el resumen cada mañana, sin quemar tokens.

## Stack

| Pieza | Tecnología |
| --- | --- |
| Worker (agente, bot, webhooks) | **Go 1.24**, solo librería estándar: un binario sin dependencias |
| Panel | Next.js 15 + React 19 |
| Datos | Supabase (Postgres + pgvector + RLS); el worker usa la API REST |
| IA | Anthropic o cualquier API compatible con OpenAI (DeepSeek, OpenRouter, Ollama). BYOK listo |
| Canales | Telegram (Bot API), Gmail (API REST + Pub/Sub) |

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
  internal/telegram     Bot API, comandos y tarjetas de aprobación
  internal/server       webhook de Gmail y /health
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
   - En Google Cloud: habilitá Gmail API y Pub/Sub, creá un OAuth client y sacá el refresh token del dueño (scopes `gmail.readonly` y `gmail.send`).
   - Creá un topic, dale permiso de publicación a `gmail-api-push@system.gserviceaccount.com` y una suscripción push a `https://TU-WORKER/webhooks/gmail?token=GMAIL_PUSH_TOKEN`.
   - El worker registra y renueva el watch solo; para probarlo a mano: `make watch`.
6. Panel: `pnpm install && pnpm dev:web` → http://localhost:3000

## En el VPS

```
cd apps/worker && make build        # bin/mano, binario estático para Linux
# o con Docker:
docker build -t mano-worker apps/worker
docker run -d --restart=always --env-file .env -p 8787:8787 mano-worker
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

Las tarjetas de aprobación traen **Aprobar**, **Descartar** y **No me avises más de este remitente**, que crea una regla: ese remitente deja de gastar IA.

## Verificación

```
cd apps/worker && make test   # go vet + tests con -race (incluye flujo completo mail → aprobación → envío)
pnpm typecheck                # panel
```

## Próximo (Fase 1)

Embeddings en `memory_chunks`, WhatsApp Cloud API, login en el panel con Supabase Auth, cifrado de tokens por tenant y llamadas proactivas.
