# Mano — Documento de producto

> Copia del documento vivo (Claude Docs, 15/10/2026). Nombre de trabajo original: "Jarvis"; nombre elegido: **Mano**.
> **Decisiones posteriores que mandan sobre este texto:** el worker se implementó en **Go** (no Node/TypeScript) y habla con Supabase por REST; el panel sigue en Next.js. Ver `README.md` y `CLAUDE.md`.

## Visión y tesis

Mano es un equipo de empleados digitales que opera el negocio desde WhatsApp, Telegram o voz: lee, decide dentro de reglas y ejecuta, pidiendo aprobación solo en lo que importa.

- **Para quién:** dueños de PyMEs y profesionales independientes (5–50 personas) que hoy son el cuello de botella de cada mensaje, cobro y agenda.
- **Promesa:** "Le hablás como a un empleado, te devuelve trabajo terminado". No un chat: resultados medibles (cobros recuperados, leads respondidos, horas ahorradas).
- **Por qué ahora:** los modelos ya ejecutan tareas de varios pasos con herramientas reales, MCP estandarizó las integraciones y el costo por tarea cayó lo suficiente para vender a precio PyME con margen.
- **Por qué nosotros:** experiencia propia en WhatsApp Cloud API, SaaS multi-tenant (NEXORA, Hábito) y clientes PyME reales en Argentina para validar rápido.

## Nombre y marca

Elegido: **Mano** (heymano.ai), "tu mano derecha que nunca duerme". Corto, se pronuncia igual en español, inglés y portugués, y ya lleva la idea de ayuda y confianza.

| Nombre | Qué transmite | Mercados | Dominio libre | Contra |
| --- | --- | --- | --- | --- |
| **Mano** | Mano derecha, ayuda, cercanía | ES, EN, PT | heymano.ai | mano.ai y mano.app ocupados; verificar marca |
| Listo | Trabajo terminado | ES, PT | heylisto.ai, listoya.ai | listo.ai ocupado |
| Manolo | Personaje con nombre propio | ES | manolo.ai | Poco global |
| Mandalo | "Mandámelo y lo resuelvo" | LatAm | mandalo.ai | Difícil en inglés |
| Dale | Acción; muy argentino | LatAm | usedale.ai | dale.ai ocupado |
| Encargado | El que se ocupa de todo | ES | encargado.ai | Largo |

Dominios consultados en el registro de Vercel el 15/10/2026. Antes de comprar: búsqueda de marca en INPI, USPTO y EUIPO, y handles en Instagram, X y TikTok.

- **Tagline ES:** Tu mano derecha que nunca duerme. **EN:** Your right hand that never sleeps.
- **Voz:** cercana, resolutiva, breve; un socio de confianza, no un robot.
- **Uso natural:** "Mano, ¿qué pasó hoy?", "Hey Mano, cobrale a Pérez".
- **Identidad visual:** mano abierta estilizada; papel, serif (Fraunces), acento terracota. Ver `docs/design/`.

## Azaris y competencia

Azaris valida el mercado pero deja huecos: no tiene WhatsApp, cobra en dólares a precio US y sus agentes son generalistas sin playbooks por industria ([azaris.ai](https://azaris.ai), 15/10/2026).

| Producto | Qué hace | Precio | Hueco |
| --- | --- | --- | --- |
| [Azaris](https://azaris.ai) | Agentes en Telegram, iMessage, Slack, Discord; 1.000+ integraciones vía Composio; memoria, browser, aprobaciones | 97 USD/mes; Done-For-You a medida | Sin WhatsApp, sin español nativo, sin facturación local, sin verticales |
| Lindy, Relay, Zapier Agents | Constructores de agentes y workflows | 20–200 USD/mes (aprox.) | Hay que armar flujos |
| ChatGPT / Claude con conectores | Asistente general | 20 USD/mes (aprox.) | No vive en WhatsApp ni actúa siempre encendido |
| Bots de WhatsApp locales | Atención y ventas por WhatsApp | Variable | Flujos rígidos; no hacen trabajo interno |

Precios "aprox." son de memoria: verificar.

## Diferenciadores

1. **WhatsApp-first** (más Telegram, Slack, iMessage, voz).
2. **Dos caras en un agente:** interna (inbox, agenda, cobros) y externa (atiende clientes por WhatsApp con tu voz de marca).
3. **Playbooks verticales:** inmobiliaria, consultorio, gastronomía, e-commerce, servicios profesionales, iglesias/ONG.
4. **Integraciones locales:** Mercado Pago, ARCA, Tienda Nube, Google Business, Open Finance; después Stripe, QuickBooks, Shopify.
5. **Panel de resultados:** plata recuperada, leads respondidos, horas ahorradas y costo de IA.
6. **Determinismo primero, IA después:** costo por tarea 3–5 veces menor.
7. **Control:** aprobaciones por umbral, registro auditable, deshacer donde se pueda.
8. **Memoria del negocio** corregible en lenguaje natural.
9. **Precio por mercado** (PPP, moneda local).
10. **White-label para agencias.**

### Modo proactivo: Mano te llama

| Urgencia | Ejemplo | Cómo avisa |
| --- | --- | --- |
| Crítica | Cliente grande enojado, pago rechazado, sitio caído | Llamada telefónica con voz natural; respondés hablando y ejecuta |
| Alta | Lead calificado, cobro grande, reunión movida | Nota de voz + tarjeta con acciones |
| Normal | Leads encontrados, resumen de inbox | En el brief matinal o de cierre |

Reglas del dueño en lenguaje natural ("llamame solo si escribe Liliana o si entra un pago mayor a 500.000"), horario de silencio, máximo de llamadas por día, bidireccional. Stack: Twilio o Telnyx + voz en tiempo real. Entra en v1 como función estrella de la demo.

## Autonomía: un Jarvis real, pero práctico

Mano trabaja por objetivos: planifica, ejecuta, verifica y aprende; pregunta solo cuando el riesgo lo justifica.

| Le decís | Mano por su cuenta |
| --- | --- |
| "Que no se me caiga ningún cobro" | Cruza facturas con pagos, recordatorios escalonados, te llama si un cliente grande pasa 30 días |
| "Conseguime leads de inmobiliarias en Mar del Plata" | Busca, califica, arma la lista, redacta el primer contacto y reporta |
| "Ordená mi semana" | Lee mails y agenda, bloquea foco, mueve choques, te deja 3 decisiones |

**Ciclo:** percibe → entiende (memoria) → planifica → actúa (según autonomía) → verifica → aprende (cada corrección es una regla).

| Nivel | Qué hace | Cuándo sube |
| --- | --- | --- |
| 0 · Observa | Detecta y sugiere | Al conectar una cuenta |
| 1 · Prepara | Deja todo listo para aprobar | Semanas sin rechazos |
| 2 · Ejecuta y avisa | Hace lo reversible y avisa | Tras ~20 aprobaciones sin editar, propone subir |
| 3 · Piloto automático | Todo dentro de límites | Solo si el dueño lo habilita |

Nivel por dominio (inbox, cobros, agenda, ventas, atención). Límites duros: pagos sobre umbral, borrar datos, firmar contratos, hablar por el dueño con alguien nuevo en temas sensibles.

**Adaptable:** descubre conectores MCP, usa browser como último recurso, guarda rutinas que repite. **Útil, no molesto:** no pregunta lo que puede averiguar, junta preguntas, respuestas de 3 líneas, aprende qué avisos ignorás.

### Proactivo sin quemar tokens

Cascada por evento: **webhooks** (nunca polling) → **reglas sin IA** → **clasificador barato** (DeepSeek Flash, Haiku, embeddings) → **modelo top solo para actuar** → **aprende** (más reglas, menos IA).

Técnicas: solo diferencias, tandas para lo no urgente, caché de prompts, memoria resumida, resultados de herramientas recortados, lo determinístico en código.

| Gobernador de gasto | Cómo funciona |
| --- | --- |
| Tope diario y mensual | En dinero, configurable |
| Degradación | 80% → modelos baratos para lo no crítico; 100% → solo lo crítico |
| Medidor en vivo | Gasto de hoy, en qué y cuánto ahorró |
| Costo antes de tareas grandes | "Esta investigación cuesta unos USD 0,40, ¿sigo?" |
| Anomalía | Si el gasto se dispara, corta y avisa |

## Mercados y go-to-market

| Ola | Mercado | Canal | Por qué |
| --- | --- | --- | --- |
| 1 | Argentina | Clientes actuales, referidos, demos | Validación rápida |
| 2 | México, Colombia, Chile, España | Agencias white-label, contenido, partners de Mercado Pago | WhatsApp dominante |
| 3 | Brasil | Partner local, portugués | Mayor mercado de WhatsApp Business |
| 4 | US / UK / UE | Self-serve, comunidades, afiliados | Ticket alto |

Motores: demo en vivo por WhatsApp, afiliados (Azaris paga 40%), casos con números, plan Done-For-You.

## Funcionalidades

**MVP:** Telegram y WhatsApp con texto y voz · Gmail y Calendar · memoria · brief matinal · tarjetas de aprobación · registro con costo.

**v1:** onboarding self-serve · atención a clientes por WhatsApp con traspaso a humano · Mercado Pago, Notion, Drive, CRM, ARCA · browser · panel · 3 playbooks · cobro en moneda local · llamadas proactivas.

**v2:** multiagente con roles · recepcionista de voz · marketplace y white-label · app móvil y modo equipo · on-premise o región UE.

### Más capacidades: del mensaje al cobro

Un cliente pregunta por WhatsApp → propuesta con tu marca → seguimiento → contrato y firma → link de pago → factura y conciliación. El dueño solo aprueba.

| Área | Capacidades | Versión |
| --- | --- | --- |
| Documentos | Propuestas, contratos desde plantillas aprobadas, recibos, informes | v1 |
| Firma | Firma electrónica con seguimiento | v1 |
| Cobros | Links de Mercado Pago y Stripe, cuotas, suscripciones, recordatorios | v1 |
| Facturación | Factura ARCA al acreditarse; notas de crédito | v1 |
| Ventas | CRM que se llena solo, seguimiento de presupuestos, campañas | v1 |
| Finanzas | Ticket a planilla, conciliación, flujo de caja, vencimientos | v2 |
| Reputación y redes | Reseñas de Google, posteos | v2 |
| Reuniones | Transcribe, resume y crea tareas | v2 |
| Operaciones | Turnos, stock, proveedores | v2 |
| Trámites | Portales sin API con aprobación | v2 |
| Conocimiento interno | Responde al equipo; onboarding | v2 |

Contratos: siempre desde plantillas revisadas por un abogado; Mano nunca firma por el dueño.

### Módulo fiscal

Factura al cobrar, controla topes y recategorización, calendario de vencimientos con VEP, lee la cuenta tributaria e intimaciones, borradores de DDJJ. El contador aprueba lo que se presenta.

- **Argentina:** web services oficiales de ARCA; lo demás por **delegación de servicios a la CUIT de la empresa** (Administrador de Relaciones), nunca compartiendo la clave fiscal. ARBA igual.
- **Otros países:** México (SAT, PAC), Chile (SII), Colombia (DIAN), Brasil (NF-e/NFS-e), España (Verifactu), EE.UU. (QuickBooks/Xero + Avalara). Verificar cada integración antes de lanzar.
- **Canal de venta:** panel para contadores con comisión recurrente.

### Onboarding en 10 minutos (por WhatsApp)

1. El dueño le escribe al número de Mano. 2. Negocio, rubro y país → playbook. 3. Conecta Gmail y Calendar; Mercado Pago y ARCA opcionales. 4. Lee 90 días y arma la memoria. 5. Propone tres misiones ("tenés 4 cobros vencidos, ¿me ocupo?"). 6. Arranca en nivel 1 y el diagnóstico de 7 días. 7. Día 7: informe de horas y pesos ahorrados, y oferta del plan.

## Arquitectura

Canales (WhatsApp, Telegram, teléfono, Slack/iMessage, panel) → gateway de mensajes (normaliza, cola, idempotencia, voz a texto) → orquestador (reglas primero, ruteo de modelos) → herramientas (MCP, Gmail, Calendar, Mercado Pago, ARCA) · memoria (Supabase + pgvector) · vigilancia (webhooks, crons, urgencia) · aprobaciones (botones, umbrales, auditoría). La vigilancia dispara avisos y llamadas proactivas.

**Núcleo técnico:** todo es un evento que entra, una misión que lo interpreta y una acción que queda registrada. Entidades: tenants, members, connections, events, missions, tasks, approvals, actions_log (inmutable), memory_facts, memory_chunks, rules, budgets/usage. Estados de tarea: recibida → filtrada → clasificada → planificada → esperando ok → ejecutando → verificando → hecha / fallida / escalada.

### BYOK y modo programador

- El cliente conecta su API key (Anthropic, OpenAI, Google, OpenRouter) o un modelo local (Ollama); paga la IA directo al proveedor.
- Suscripciones de consumo (Claude Pro/Max, ChatGPT Plus) no sirven como motor de una app de terceros.
- Keys cifradas por cliente; límite de gasto.
- **Modo programador:** conecta GitHub, corre un agente de código en sandbox, abre PRs; nunca hace merge sin aprobación.
- Ruteo: DeepSeek Flash (~0,15–0,30 USD/M entrada, 0,60–1,20 salida, mitad fuera de hora pico; verificar) o Haiku para triage; Sonnet/Opus para decidir, redactar y programar.

### Ideas en evaluación

| Idea | Qué hace | Versión |
| --- | --- | --- |
| **Red Mano** | Agentes de distintas empresas cotizan, confirman, coordinan y concilian entre sí (efecto red) | v2 |
| Diagnóstico gratis | Observa una semana y entrega horas y pesos que ahorraría | v1 |
| Modo ausencia | Cubre el negocio si el dueño viaja o se enferma | v1 |
| Salud del negocio | Detecta anomalías y propone acciones | v2 |
| Antifraude | Phishing, facturas falsas, CBU cambiados | v1 |
| Relación con clientes | Cumpleaños, recompras | v2 |
| Compras y proveedores | Compara y compra con aprobación | v2 |
| Búsquedas de personal | Publica, filtra CV, coordina | v2 |
| Vida personal | VTV, seguros, turnos, trámites | v2 |
| Un Mano por persona | Agentes por empleado con memoria compartida | v2 |
| Auto y reloj | Android Auto, CarPlay, smartwatch | v2 |
| Marketplace de habilidades | Rutinas y playbooks con comisión | v2 |

Red Mano: identidad verificada por CUIT, mensajes estructurados sobre protocolos abiertos agente a agente, cada lado con sus reglas, invitación viral cuando el otro no usa Mano, datos mínimos.

## Economía y pricing

Objetivo: margen bruto ≥ 70%, costo variable < 15 USD/mes en el plan base.

| Plan | US/EU | LatAm (landing actual) | Incluye |
| --- | --- | --- | --- |
| Solo | 49 USD | 25 USD | 1 agente, mensajería, Gmail/Calendar, brief |
| Pro | 129 USD | 69 USD | WhatsApp con clientes, llamadas, playbook, cobros |
| Equipo | 349 USD | 199 USD | Varios agentes y usuarios, contador, reportes |
| A medida | Desde 1.500 USD setup | Desde 800 USD setup | Done-For-You |

BYOK: Solo 15, Pro 45, Equipo 149 USD/mes; margen ~90%.

Costo variable Pro (a validar): LLM 6–12 · WhatsApp 2–8 (desde 1-oct-2026 se cobra también el servicio) · llamadas 2–5 · infra 2–4 USD/mes. Margen estimado: Solo 43–70%, Pro 53–78%, Equipo 60–78%; con topes, créditos por exceso y ruteo barato, 70%+. Solo a 25 USD queda justo: subir a 29 o recortar cupo.

Costos fijos: en Fase 0 casi cero (dominio + VPS ~160.000 ARS/año). Con clientes pagos: Vercel Hobby no permite uso comercial (Pro ~20 USD/mes) y Supabase Free pausa proyectos y no tiene backups (Pro ~25 USD/mes); alternativa: todo en el VPS. Twilio no es gratis para producción.

Sociedad: armar la sociedad con el socio antes de facturar en serio (el monotributo queda chico).

## Seguridad y cumplimiento

Permisos mínimos por herramienta · defensa contra prompt injection (contenido externo = dato) · RLS y tokens cifrados por tenant · auditoría inmutable · Ley 25.326, LGPD, GDPR, políticas de WhatsApp Business · camino a SOC 2 · no entrenar con datos del cliente, borrado a pedido, región UE en v2.

## Roadmap

| Fase | Cuándo | Qué | Gate para avanzar |
| --- | --- | --- | --- |
| 0 · Mano personal | Mes 1 | Telegram + Gmail, brief, memoria | Usarlo 30 días sin dejarlo |
| 1 · Pilotos | Meses 2–3 | WhatsApp + voz, avisos por llamada, 2 clientes | 2 pilotos pagan con ahorro medible |
| 2 · Lanzamiento AR | Meses 4–6 | Alta self-serve, 3 playbooks, cobro en pesos | 50 clientes pagos, churn < 5% |
| 3 · LatAm y Brasil | Meses 7–12 | Agencias, portugués, multiagente | 300 clientes, MRR 20.000 USD |
| 4 · Global | Año 2 | Self-serve en USD, región UE, SOC 2 | — |

Métricas: norte = horas ahorradas y plata recuperada por cliente por semana; activación = primera tarea aprobada < 24 h; retención a 90 días; costo de IA y margen mensual.

**Próximos pasos inmediatos:** comprar heymano.ai y handles, búsqueda en INPI · landing online con lista de espera · validar con 5 PyMEs conocidas · Fase 0 en uso propio.

## Riesgos y preguntas abiertas

| Riesgo | Mitigación |
| --- | --- |
| Las grandes lanzan algo parecido gratis | Profundidad vertical, integraciones locales, servicio |
| Meta restringe bots de IA generalistas | Posicionar como agente de negocio; Telegram y voz de respaldo |
| Costo de IA con usuarios intensivos | Ruteo, determinismo, créditos por exceso |
| Error del agente con impacto real | Aprobaciones, deshacer, auditoría, seguro |
| Proyecto de una persona | MVP angosto, reutilizar NEXORA/Hábito, socio comercial |

- [x] Nombre: Mano
- [ ] ¿Primer vertical: inmobiliaria (NEXORA ya existe) u otro?
- [ ] ¿Integraciones propias o Composio para arrancar?
- [ ] ¿Qué cliente actual entra como piloto pago?
- [ ] ¿Financiación propia o pre-seed tras los pilotos?
