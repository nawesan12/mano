package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

const ruleSystem = `Traducís un pedido del dueño de un negocio a UNA regla de Mano en JSON. No inventes condiciones.
Formato: {"match":{...},"outcome":{...},"summary":"la regla en una línea, de vos"}
match (al menos un campo):
- "source": "gmail" | "whatsapp" | "mercadopago" | "calendar" | "mercadolibre" (solo si lo dice o es obvio: pagos = mercadopago, ventas y envíos = mercadolibre)
- "kind": solo para pagos: "payment.approved" (acreditado), "payment.rejected", "payment.charged_back" (contracargo)
- "senderIncludesAny": ["nombres, mails o teléfonos"]
- "textIncludesAny": ["palabras clave"]
- "amountGte": número (montos en pesos, sin puntos: 500.000 → 500000)
outcome:
- "urgency": "critical" | "high" | "normal" | "ignore"
- "notify": "call" si pide que lo llames; "silent" si pide que no le avises al momento
- "ignoreQuiet": true solo si pide que lo llames aunque sea de noche o en horario de silencio
- "needsAction": true si hay que responder algo
Si el pedido no es una regla, devolvé {"error":"qué falta, en una línea"}.`

type ruleDraft struct {
	Match   json.RawMessage `json:"match"`
	Outcome json.RawMessage `json:"outcome"`
	Summary string          `json:"summary"`
	Error   string          `json:"error"`
}

// ProposeRule traduce "llamame si escribe Liliana" a una regla validada y la deja para aprobar.
// La regla no se guarda hasta que el dueño la aprueba.
func (a *Agent) ProposeRule(ctx context.Context, text string) (string, *store.Approval) {
	r, err := a.models.Complete(ctx, core.Cheap, core.Request{
		System: ruleSystem, Messages: []core.Message{{Role: "user", Content: core.Truncate(text, 600)}}, MaxTokens: 300, JSON: true,
	}, CallOpts{})
	if b, ok := IsBlocked(err); ok {
		return b.Reason, nil
	}
	var d ruleDraft
	if err == nil {
		err = core.ParseJSON(r.Text, &d)
	}
	if err != nil {
		return "No pude entender la regla. Probá de nuevo con otras palabras.", nil
	}
	if d.Error != "" {
		return "No la armé: " + d.Error, nil
	}
	if _, err := core.ParseRule("nueva", 50, d.Match, d.Outcome); err != nil {
		return "No me salió una regla válida (" + err.Error() + "). Probá de nuevo más concreto.", nil
	}
	spec, _ := json.Marshal(map[string]json.RawMessage{"match": d.Match, "outcome": d.Outcome})
	summary := strings.TrimSpace(d.Summary)
	if summary == "" {
		summary = core.Truncate(text, 80)
	}
	ap := store.Approval{Area: "Reglas", Risk: "bajo", Title: core.Truncate("Regla: "+summary, 90),
		Detail: "Así la entendí:\n" + string(spec), Action: store.Action{Type: "rule.add", Text: string(spec)}}
	id, err := a.store.CreateApproval(ctx, ap)
	if err != nil {
		return "No pude guardarla para que la apruebes: " + err.Error(), nil
	}
	ap.ID = id
	return "", &ap
}

// applyRule guarda la regla aprobada. Se vuelve a validar: lo que está en la base es dato.
func (a *Agent) applyRule(ctx context.Context, ap store.Approval, by string) string {
	var spec struct {
		Match   json.RawMessage `json:"match"`
		Outcome json.RawMessage `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(ap.Action.Text), &spec); err != nil {
		return "La regla estaba rota, no la guardé."
	}
	rule, err := core.ParseRule("nueva", 50, spec.Match, spec.Outcome)
	if err != nil {
		return "La regla no es válida, no la guardé: " + err.Error()
	}
	if err := a.store.AddRule(ctx, rule.Match, rule.Outcome, "owner"); err != nil {
		return "No pude guardar la regla: " + err.Error()
	}
	_ = a.store.Log(ctx, store.LogEntry{Area: "Reglas", Tool: "rules.add", Summary: ap.Title, Via: "cheap", ApprovedBy: by})
	return fmt.Sprintf("Listo, regla activa. %s", strings.TrimPrefix(ap.Title, "Regla: "))
}
