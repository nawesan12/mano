# Documentación de Mano

| Archivo | Qué es |
| --- | --- |
| [producto.md](producto.md) | Documento de producto: visión, competencia, diferenciadores, autonomía, pricing, roadmap y riesgos |
| [design/landing.html](design/landing.html) | Landing animada: mano de partículas (Three.js) que muta por sección, Lenis, GSAP/ScrollTrigger |
| [design/panel-preview.html](design/panel-preview.html) | Vista previa interactiva del panel con datos de ejemplo: aprobaciones, misiones, actividad, gasto, conexiones, memoria, chat y llamada simulada |
| [design/landing-v1.dc.html](design/landing-v1.dc.html) | Primera landing estática, en formato de lienzo de Claude Design |

## Originales en claude.ai

Son privados; abrilos con tu cuenta.

- Documento de producto (vivo): https://claude.ai/code/artifact/36e0925d-03e6-4032-b5bd-fab468a1bf1f
- Landing animada: https://claude.ai/artifact/1bQwYd4J5jHZFvXYJzDdCD
- Vista previa del panel: https://claude.ai/artifact/SX1NsPKvaiyfwSaT8c9m8m
- Lienzo de la landing v1: https://claude.ai/artifact/Bvtfr1WgQNJNbmDmHNqnYx

## Cómo verlos

Los `.html` son páginas autocontenidas (fuentes de Google Fonts y librerías por CDN). Abrilos directo en el navegador o servilos con `npx serve docs/design`. No traen `<!doctype>` porque se publicaron como artifacts; el navegador los renderiza igual.

## Sistema visual

- Fondo papel `#F2EDE3`, tinta `#1A1916`, acento terracota `#A8441F` (oscuro: `#E07A52`).
- Tipografías: Fraunces (títulos), Geist (texto), JetBrains Mono (etiquetas y números).
- Sin gradientes ni emojis. Modo oscuro por tokens CSS.
- Personaje: una mano abierta, en partículas en la landing y en trazo en el logo.

Para pasar la landing a producción: Next.js + React Three Fiber, con un modelo 3D de la mano con morph targets hecho en Blender.
