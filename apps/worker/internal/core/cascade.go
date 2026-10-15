package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

const classifierSystem = `Sos el filtro de entrada de Mano, el asistente de un dueño de PyME.
Clasificá UN evento. Criterios:
- critical: dinero en riesgo (pago sospechoso, cambio de datos bancarios), cliente importante enojado, intimación legal o fiscal, algo que se rompe hoy.
- high: lead nuevo con intención real, pago grande acreditado, reunión movida o cancelada, pedido de presupuesto con fecha.
- normal: información útil que puede esperar al resumen diario.
- ignore: newsletters, promociones, notificaciones automáticas sin acción.
needsAction = true si hace falta responder, cobrar, agendar o decidir algo.
Devolvé JSON: {"urgency":"…","needsAction":bool,"intent":"2 a 5 palabras","summary":"una línea en español rioplatense"}`

var spaces = regexp.MustCompile(`\s+`)

// CompactEvent recorta el evento: al clasificador va lo justo, nunca el HTML completo.
func CompactEvent(ev Event, maxBody int) string {
	body := truncate(strings.TrimSpace(spaces.ReplaceAllString(ev.Body, " ")), maxBody)
	lines := []string{"origen: " + ev.Source + "/" + ev.Kind}
	if ev.Sender != "" {
		lines = append(lines, "de: "+ev.Sender)
	}
	if ev.Subject != "" {
		lines = append(lines, "asunto: "+ev.Subject)
	}
	if ev.Amount != nil {
		lines = append(lines, fmt.Sprintf("monto: %.2f", *ev.Amount))
	}
	return strings.Join(append(lines, "contenido: "+body), "\n")
}

func fallbackSummary(ev Event) string {
	if ev.Subject != "" {
		return ev.Subject
	}
	return truncate(ev.Body, 120)
}

// Triage: 1) reglas (gratis) → 2) clasificador barato. El modelo top nunca clasifica.
// cheap puede ser nil (sin presupuesto o sin modelo): el evento queda para el resumen.
func RunTriage(ctx context.Context, ev Event, rules []Rule, cheap LLM) Triage {
	if r := FirstMatch(rules, ev); r != nil {
		s := r.Outcome.Note
		if s == "" {
			s = fallbackSummary(ev)
		}
		intent := r.Outcome.Note
		if intent == "" {
			intent = "regla"
		}
		return Triage{Urgency: r.Outcome.Urgency, NeedsAction: r.Outcome.NeedsAction, Intent: intent, Summary: s, Via: "rule", RuleID: r.ID}
	}
	if cheap == nil {
		return Triage{Urgency: Normal, Intent: "sin clasificar", Summary: fallbackSummary(ev), Via: "rule"}
	}
	res, err := cheap.Complete(ctx, Request{System: classifierSystem, Messages: []Message{{Role: "user", Content: CompactEvent(ev, 1200)}}, MaxTokens: 160, JSON: true})
	if err != nil {
		return Triage{Urgency: Normal, Intent: "error al clasificar", Summary: fallbackSummary(ev), Via: "classifier"}
	}
	var out struct {
		Urgency     Urgency `json:"urgency"`
		NeedsAction bool    `json:"needsAction"`
		Intent      string  `json:"intent"`
		Summary     string  `json:"summary"`
	}
	bad := ParseJSON(res.Text, &out) != nil || out.Summary == ""
	switch out.Urgency {
	case Critical, High, Normal, Ignore:
	default:
		bad = true
	}
	if bad {
		return Triage{Urgency: Normal, Intent: "no se pudo clasificar", Summary: fallbackSummary(ev), Via: "classifier", CostUSD: res.CostUSD}
	}
	return Triage{Urgency: out.Urgency, NeedsAction: out.NeedsAction, Intent: truncate(out.Intent, 60), Summary: truncate(out.Summary, 240), Via: "classifier", CostUSD: res.CostUSD}
}

// NeedsTop: solo se usa el modelo top si hay que actuar y no es ruido.
func NeedsTop(t Triage) bool { return t.NeedsAction && t.Urgency != Ignore }
