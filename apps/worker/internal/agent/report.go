package agent

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// Minutos que le ahorra al dueño cada cosa. Estimaciones a la vista, a propósito conservadoras.
const (
	minPerEvent   = 1.0 // leer y clasificar un mail o mensaje
	minPerIgnored = 0.5 // un newsletter que no tuvo que abrir
	minPerReply   = 6.0 // escribir una respuesta
	minPerCall    = 3.0 // enterarse y decidir por teléfono en vez de leer todo
	minPerCharge  = 5.0 // armar y mandar un link de cobro, y seguirlo
	minPerFraud   = 10.0
)

// Report es el diagnóstico de un período. Todo sale de la base: sin IA.
type Report struct {
	From, To                          time.Time
	Events, Ignored, NoAI             int
	Critical, High                    int
	Replies, Approved, Rejected       int
	Calls, CallMinutes, CustomerCalls int
	ChargesSent, ChargesPaid          int
	Collected                         float64
	FraudAlerts                       int
	AICostUSD, WACostUSD              float64
	SavedMinutes                      float64
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		var f float64
		_, _ = fmt.Sscan(x, &f)
		return f
	}
	return 0
}

// BuildReport arma el diagnóstico desde since hasta ahora.
func (a *Agent) BuildReport(ctx context.Context, since time.Time) (Report, error) {
	r := Report{From: since, To: time.Now()}
	events, err := a.store.Since(ctx, "events", "urgency,triaged_via", "received_at", since)
	if err != nil {
		return r, err
	}
	for _, e := range events {
		r.Events++
		switch e["urgency"] {
		case "ignore":
			r.Ignored++
		case "critical":
			r.Critical++
		case "high":
			r.High++
		}
		if e["triaged_via"] == "rule" {
			r.NoAI++
		}
	}
	logs, _ := a.store.Since(ctx, "actions_log", "tool,area", "created_at", since)
	for _, l := range logs {
		switch l["tool"] {
		case "gmail.reply", "whatsapp.reply":
			r.Replies++
		case "mp.link":
			r.ChargesSent++
		}
	}
	approvals, _ := a.store.Since(ctx, "approvals", "status,area", "created_at", since)
	for _, ap := range approvals {
		switch ap["status"] {
		case "approved":
			r.Approved++
		case "rejected":
			r.Rejected++
		}
		if ap["area"] == "Antifraude" {
			r.FraudAlerts++
		}
	}
	calls, _ := a.store.Since(ctx, "calls", "status,duration_s,party,wa_cost_usd", "created_at", since)
	for _, c := range calls {
		r.WACostUSD += num(c["wa_cost_usd"])
		if c["status"] != "completed" {
			continue
		}
		r.CallMinutes += int(math.Ceil(num(c["duration_s"]) / 60))
		if c["party"] == "customer" {
			r.CustomerCalls++
		} else {
			r.Calls++
		}
	}
	charges, _ := a.store.Since(ctx, "mp_charges", "status,amount", "created_at", since)
	for _, c := range charges {
		if c["status"] == "paid" {
			r.ChargesPaid++
			r.Collected += num(c["amount"])
		}
	}
	usage, _ := a.store.Since(ctx, "usage", "cost_usd", "created_at", since)
	for _, u := range usage {
		r.AICostUSD += num(u["cost_usd"])
	}
	r.SavedMinutes = float64(r.Events-r.Ignored)*minPerEvent + float64(r.Ignored)*minPerIgnored + float64(r.Replies)*minPerReply +
		float64(r.Calls+r.CustomerCalls)*minPerCall + float64(r.ChargesSent)*minPerCharge + float64(r.FraudAlerts)*minPerFraud
	return r, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func hm(min float64) string {
	h, m := int(min)/60, int(math.Round(min))%60
	switch {
	case h == 0:
		return fmt.Sprintf("%d min", m)
	case m == 0:
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

func pesos(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	return "$" + b.String()
}

// Text es el diagnóstico para el chat: corto, con números.
func (r Report) Text() string {
	if r.Events == 0 {
		return "Esta semana no entró nada para que yo atienda. Conectá Gmail o WhatsApp y te muestro lo que te ahorro."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Tu semana con Mano: entraron %d cosas y %d%% las resolví sin IA.", r.Events, r.NoAI*100/r.Events)
	if r.Critical+r.High > 0 {
		fmt.Fprintf(&b, " %d eran importantes o críticas.", r.Critical+r.High)
	}
	var did []string
	if r.Replies > 0 {
		did = append(did, "respondí "+plural(r.Replies, "mensaje", "mensajes"))
	}
	if r.Calls > 0 {
		did = append(did, "te llamé "+plural(r.Calls, "vez", "veces"))
	}
	if r.CustomerCalls > 0 {
		did = append(did, "llamé a "+plural(r.CustomerCalls, "cliente", "clientes"))
	}
	if r.ChargesPaid > 0 {
		did = append(did, fmt.Sprintf("cobraste %s por %s", pesos(r.Collected), plural(r.ChargesPaid, "link", "links")))
	}
	if r.FraudAlerts > 0 {
		did = append(did, "frené "+plural(r.FraudAlerts, "cuenta bancaria sospechosa", "cuentas bancarias sospechosas"))
	}
	if len(did) > 0 {
		b.WriteString("\n" + strings.ToUpper(did[0][:1]) + did[0][1:])
		for i, d := range did[1:] {
			if i == len(did)-2 {
				b.WriteString(" y " + d)
			} else {
				b.WriteString(", " + d)
			}
		}
		b.WriteString(".")
	}
	fmt.Fprintf(&b, "\nTe ahorré unas %s. Me costó USD %.2f de IA", hm(r.SavedMinutes), r.AICostUSD)
	if r.WACostUSD > 0 {
		fmt.Fprintf(&b, " y USD %.2f de llamadas", r.WACostUSD)
	}
	b.WriteString(".")
	return b.String()
}

// WeeklyReport arma y guarda el diagnóstico de los últimos 7 días.
func (a *Agent) WeeklyReport(ctx context.Context) (string, error) {
	now := time.Now().In(a.loc)
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.loc).AddDate(0, 0, -7)
	r, err := a.BuildReport(ctx, since)
	if err != nil {
		return "", err
	}
	text := r.Text()
	_ = a.store.SaveReport(ctx, since, r, text)
	return text, nil
}
