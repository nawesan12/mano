package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

// senderKey identifica al remitente para recordar sus cuentas: el mail o el teléfono, nunca el nombre (se falsifica fácil).
func senderKey(ev core.Event) string {
	switch ev.Source {
	case "gmail":
		if m := strings.ToLower(emailOf("", ev.Sender)); strings.Contains(m, "@") {
			return "mail:" + m
		}
	case "whatsapp":
		if p, _ := ev.Payload["phone"].(string); p != "" {
			return "wa:" + p
		}
	}
	return ""
}

// checkAccounts compara las cuentas del mensaje con las que ese remitente usó antes. Sin IA.
// La primera vez se confía (y se guarda); una distinta después es crítica hasta que el dueño la confirme.
func (a *Agent) checkAccounts(ctx context.Context, ev core.Event) (core.Triage, []string, bool) {
	key := senderKey(ev)
	if key == "" {
		return core.Triage{}, nil, false
	}
	found := core.ExtractAccounts(ev.Subject + "\n" + ev.Body)
	if len(found) == 0 {
		return core.Triage{}, nil, false
	}
	known, err := a.store.KnownAccounts(ctx, key)
	if err != nil {
		return core.Triage{}, nil, false
	}
	trusted, seen := map[string]bool{}, map[string]bool{}
	var old []string
	for _, k := range known {
		seen[k.Account] = true
		if k.Trusted {
			trusted[k.Account] = true
			old = append(old, core.MaskAccount(k.Account))
		}
	}
	if len(trusted) == 0 {
		for _, acc := range found {
			_ = a.store.AddKnownAccount(ctx, key, acc, true)
		}
		return core.Triage{}, nil, false
	}
	var fresh, unseen []string
	for _, acc := range found {
		if !trusted[acc] {
			fresh = append(fresh, acc)
		}
		if !seen[acc] {
			unseen = append(unseen, acc) // la tarjeta para confirmarla sale una sola vez
			_ = a.store.AddKnownAccount(ctx, key, acc, false)
		}
	}
	if len(fresh) == 0 {
		return core.Triage{}, nil, false
	}
	masked := make([]string, len(fresh))
	for i, f := range fresh {
		masked[i] = core.MaskAccount(f)
	}
	who := ev.Sender
	if who == "" {
		who = strings.TrimPrefix(strings.TrimPrefix(key, "mail:"), "wa:")
	}
	return core.Triage{Urgency: core.Critical, NeedsAction: true, Intent: "posible fraude", Via: "rule",
		Summary: fmt.Sprintf("Ojo: %s te pasó una cuenta distinta a la de siempre (%s; antes %s). Puede ser fraude: confirmalo por teléfono antes de pagar.",
			who, strings.Join(masked, ", "), strings.Join(old, ", "))}, unseen, true
}

// proposeTrust deja para aprobar que la cuenta nueva pase a ser de confianza. Riesgo alto, solo con el botón.
func (a *Agent) proposeTrust(ctx context.Context, ev core.Event, fresh []string) {
	key := senderKey(ev)
	for _, acc := range fresh {
		ap := store.Approval{Area: "Antifraude", Risk: "alto", Title: core.Truncate("Confiar en la cuenta nueva de "+ev.Sender, 90),
			Detail: "Aprobalo solo si lo confirmaste por otro canal (llamando al número de siempre, no al del mensaje). Cuenta: " + core.MaskAccount(acc),
			Action: store.Action{Type: "account.trust", To: key, Ref: acc}}
		if id, err := a.store.CreateApproval(ctx, ap); err == nil {
			ap.ID = id
			_ = a.notify.ApprovalCard(ctx, ap)
		}
	}
}

func (a *Agent) trustAccount(ctx context.Context, ap store.Approval, by string) string {
	if err := a.store.AddKnownAccount(ctx, ap.Action.To, ap.Action.Ref, true); err != nil {
		return "No pude guardarla: " + err.Error()
	}
	_ = a.store.Log(ctx, store.LogEntry{Area: "Antifraude", Tool: "account.trust", Summary: "Confirmaste la " + core.MaskAccount(ap.Action.Ref) + " de " + ap.Action.To, Via: "code", ApprovedBy: by})
	return "Listo, esa cuenta queda como de confianza."
}
