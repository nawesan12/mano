package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// findContact busca por teléfono o por nombre (sin tildes ni mayúsculas). Varios parecidos = hay que precisar.
func (a *Agent) findContact(ctx context.Context, who string) (store.Contact, string) {
	contacts, err := a.store.Contacts(ctx)
	if err != nil {
		return store.Contact{}, "No pude leer tus contactos: " + err.Error()
	}
	// también los clientes con teléfono (por ejemplo, alguien que pagó y dejó su número)
	seen := map[string]bool{}
	for _, c := range contacts {
		seen[c.Phone] = true
	}
	if cs, err := a.store.Customers(ctx); err == nil {
		for _, c := range cs {
			if c.Phone != "" && !seen[c.Phone] {
				seen[c.Phone] = true
				contacts = append(contacts, store.Contact{Phone: c.Phone, Name: c.Name})
			}
		}
	}
	if d := digitsOnly(who); len(d) >= 8 {
		for _, c := range contacts {
			if strings.HasSuffix(c.Phone, d) || strings.HasSuffix(d, c.Phone) {
				return c, ""
			}
		}
		return store.Contact{Phone: d, Name: "+" + d}, ""
	}
	var hits []store.Contact
	for _, c := range contacts {
		if c.Name != "" && strings.Contains(core.Norm(c.Name), core.Norm(strings.TrimSpace(who))) {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 0:
		return store.Contact{}, "No encontré a " + who + " entre tus contactos de WhatsApp. Pasame el número."
	case 1:
		return hits[0], ""
	}
	names := make([]string, 0, len(hits))
	for i, h := range hits {
		if i == 4 {
			break
		}
		names = append(names, h.Name+" (+"+h.Phone+")")
	}
	return store.Contact{}, "Hay varios: " + strings.Join(names, ", ") + ". Decime cuál, o pasame el número."
}

// ProposeCustomerCall deja lista una llamada a un cliente para que el dueño la apruebe. Nada sale sin su ok.
func (a *Agent) ProposeCustomerCall(ctx context.Context, who, goal string) (string, *store.Approval) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return "Decime para qué lo llamo. Por ejemplo: /llamar Pérez: recordarle que vence la factura 123", nil
	}
	c, problem := a.findContact(ctx, who)
	if problem != "" {
		return problem, nil
	}
	name := c.Name
	if name == "" {
		name = "+" + c.Phone
	}
	ap := store.Approval{Area: "Llamadas", Risk: "medio", Title: core.Truncate("Llamar a "+name+": "+goal, 90),
		Detail: fmt.Sprintf("Llamo a %s por WhatsApp (+%s), me presento como el asistente virtual del negocio y le digo esto:\n%s\n\nNo prometo nada fuera de eso y te cuento cómo salió.", name, c.Phone, goal),
		Action: store.Action{Type: "call.customer", To: c.Phone, Subject: name, Text: core.Truncate(goal, 500)}}
	id, err := a.store.CreateApproval(ctx, ap)
	if err != nil {
		return "No pude dejarla para que la apruebes: " + err.Error(), nil
	}
	ap.ID = id
	return "", &ap
}

// startCustomerCall arranca la llamada ya aprobada. Se corre aparte: la llamada dura minutos.
func (a *Agent) startCustomerCall(ctx context.Context, ap store.Approval, by string) string {
	cc, ok := a.caller.(CustomerCaller)
	if !ok || a.caller == nil {
		_ = a.store.SetTask(ctx, ap.TaskID, "failed", map[string]any{"result": map[string]string{"error": "llamadas apagadas"}})
		return "No puedo llamar: las llamadas no están activadas (VOICE_ENABLED)."
	}
	_ = a.store.Log(ctx, store.LogEntry{Area: "Llamadas", Tool: "call.customer", Summary: "Aprobaste llamar a " + ap.Action.Subject, Via: "code", ApprovedBy: by, TaskID: ap.TaskID})
	req := CustomerCall{Phone: ap.Action.To, Name: ap.Action.Subject, Goal: ap.Action.Text, ApprovedBy: by, TaskID: ap.TaskID}
	base := context.WithoutCancel(ctx)
	a.spawn(func() {
		why, err := cc.CallCustomer(base, req)
		switch {
		case err != nil:
			slog.Error("llamada a cliente", "err", err)
			_ = a.notify.Send(base, "No pude llamar a "+req.Name+": "+err.Error())
		case why != "":
			_ = a.notify.Send(base, "No llamé a "+req.Name+": "+why+".")
		}
	})
	return "Llamo a " + ap.Action.Subject + " ahora. Te cuento cómo salió."
}
