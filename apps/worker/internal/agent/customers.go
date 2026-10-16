package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

// touchFrom saca del evento quién es el cliente. Lo ignorado (newsletters, automáticos) no es cliente.
func touchFrom(ev core.Event, t core.Triage) (store.Touch, bool) {
	if t.Urgency == core.Ignore {
		return store.Touch{}, false
	}
	str := func(k string) string { v, _ := ev.Payload[k].(string); return v }
	tc := store.Touch{Channel: ev.Source, At: ev.ReceivedAt}
	switch ev.Source {
	case "whatsapp":
		tc.Phone, tc.Name = str("phone"), str("name")
	case "gmail":
		tc.Email, tc.Name = emailOf("", ev.Sender), speakableName(ev.Sender)
	case "mercadopago":
		if !strings.Contains(ev.Sender, "@") {
			return tc, false
		}
		tc.Email, tc.Name = emailOf("", ev.Sender), speakableName(ev.Sender)
		if ev.Kind == "payment.approved" && ev.Amount != nil {
			tc.Paid = *ev.Amount
		}
	default:
		return tc, false
	}
	if !strings.Contains(tc.Email, "@") {
		tc.Email = ""
	}
	return tc, tc.Phone != "" || tc.Email != ""
}

func (a *Agent) learnCustomer(ctx context.Context, ev core.Event, t core.Triage) {
	if tc, ok := touchFrom(ev, t); ok {
		_ = a.store.TouchCustomer(ctx, tc)
	}
}

// customerContext es lo que Mano sabe de quien escribe, para redactar mejor. Es dato, no instrucciones.
func (a *Agent) customerContext(ctx context.Context, ev core.Event) string {
	phone, _ := ev.Payload["phone"].(string)
	email := ""
	if ev.Source == "gmail" {
		email = emailOf("", ev.Sender)
	}
	c, err := a.store.CustomerByPhoneOrEmail(ctx, phone, email)
	if err != nil || c == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("Cliente: %s, %d contactos", orName(c), c.Contacts)}
	if c.PaidTotal > 0 {
		parts = append(parts, "pagó en total "+pesos(c.PaidTotal))
	}
	out := strings.Join(parts, ", ") + "."
	if c.Notes != "" {
		out += "\nNotas del dueño sobre este cliente:\n" + core.Truncate(c.Notes, 1200)
	}
	return out
}

func orName(c *store.Customer) string {
	switch {
	case c.Name != "":
		return c.Name
	case c.Phone != "":
		return "+" + c.Phone
	}
	return c.Email
}

// findCustomer busca por nombre, teléfono o mail. Varios parecidos = hay que precisar.
func (a *Agent) findCustomer(ctx context.Context, who string) (*store.Customer, string) {
	all, err := a.store.Customers(ctx)
	if err != nil {
		return nil, "No pude leer tus clientes: " + err.Error()
	}
	q := core.Norm(strings.TrimSpace(who))
	d := digitsOnly(who)
	var hits []store.Customer
	for _, c := range all {
		switch {
		case len(d) >= 8 && c.Phone != "" && (strings.HasSuffix(c.Phone, d) || strings.HasSuffix(d, c.Phone)):
			return &c, ""
		case strings.Contains(q, "@") && c.Email == q:
			return &c, ""
		case c.Name != "" && strings.Contains(core.Norm(c.Name), q):
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 0:
		return nil, "No tengo a " + who + " entre tus clientes todavía."
	case 1:
		return &hits[0], ""
	}
	names := make([]string, 0, 4)
	for i, h := range hits {
		if i == 4 {
			break
		}
		names = append(names, orName(&h))
	}
	return nil, "Hay varios: " + strings.Join(names, ", ") + ". ¿Cuál?"
}

// CustomerCard es la ficha del cliente, sin IA.
func (a *Agent) CustomerCard(ctx context.Context, who string) string {
	c, problem := a.findCustomer(ctx, who)
	if c == nil {
		return problem
	}
	var b strings.Builder
	b.WriteString(orName(c))
	if c.Phone != "" {
		b.WriteString(" · +" + c.Phone)
	}
	if c.Email != "" {
		b.WriteString(" · " + c.Email)
	}
	fmt.Fprintf(&b, "\n%s", plural(c.Contacts, "contacto", "contactos"))
	if c.LastContactAt != nil {
		fmt.Fprintf(&b, ", el último %s por %s", ago(time.Since(*c.LastContactAt))+" atrás", c.LastChannel)
	}
	b.WriteString(".")
	if c.PaidTotal > 0 {
		fmt.Fprintf(&b, "\nPagó en total %s.", pesos(c.PaidTotal))
	}
	if c.Phone != "" {
		if pend, _ := a.store.PendingCharges(ctx); len(pend) > 0 {
			for _, ch := range pend {
				if ch.Phone == c.Phone {
					fmt.Fprintf(&b, "\nLink sin pagar: %s (%s).", pesos(ch.Amount), ch.Description)
				}
			}
		}
	}
	if c.Notes != "" {
		b.WriteString("\nNotas:\n" + c.Notes)
	}
	return b.String()
}

// AddNote suma una nota del dueño a la ficha del cliente.
func (a *Agent) AddNote(ctx context.Context, who, note string) string {
	c, problem := a.findCustomer(ctx, who)
	if c == nil {
		return problem
	}
	if err := a.store.AddCustomerNote(ctx, *c, note, time.Now().In(a.loc)); err != nil {
		return "No pude guardarla: " + err.Error()
	}
	return "Anotado en la ficha de " + orName(c) + ". Lo voy a tener en cuenta cuando le responda."
}
