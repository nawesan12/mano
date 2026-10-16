package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

// CustomerNotifier le escribe a un cliente (whatsapp.Client.Notify): texto en ventana, plantilla fuera.
type CustomerNotifier interface {
	Notify(ctx context.Context, to, text, template, lang string, params ...string) error
}

type reminder struct {
	Phone string `json:"phone"`
	Name  string `json:"name"`
	At    string `json:"at"` // RFC3339
	Title string `json:"title"`
}

// SetReminders activa los recordatorios de turnos. template es una plantilla utility con {{1}} nombre, {{2}} día, {{3}} hora.
func (a *Agent) SetReminders(n CustomerNotifier, template, lang string) {
	a.remind, a.remindTpl, a.remindLang = n, template, lang
}

func (a *Agent) reminderText(r reminder, business string) string {
	at, _ := time.Parse(time.RFC3339, r.At)
	at = at.In(a.loc)
	first := strings.Fields(r.Name)
	hi := "Hola"
	if len(first) > 0 && !strings.HasPrefix(first[0], "+") {
		hi += " " + first[0]
	}
	who := ""
	if business != "" {
		who = " con " + business
	}
	return fmt.Sprintf("%s, te recordamos tu turno de mañana %s a las %s%s. Si no podés venir, avisanos por acá.", hi, longDate(at), at.Format("15:04"), who)
}

// TomorrowReminders cruza los eventos de mañana con clientes que tienen teléfono y deja UNA aprobación con todos.
func (a *Agent) TomorrowReminders(ctx context.Context) (*store.Approval, error) {
	if a.remind == nil {
		return nil, nil
	}
	now := time.Now().In(a.loc)
	from := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, a.loc)
	to := from.Add(24 * time.Hour)
	evs, err := a.store.CalendarEvents(ctx)
	if err != nil {
		return nil, err
	}
	customers, err := a.store.Customers(ctx)
	if err != nil {
		return nil, err
	}
	var list []reminder
	seen := map[string]bool{}
	for _, e := range evs {
		if e.Status == "cancelled" || e.AllDay || e.Start.Before(from) || !e.Start.Before(to) {
			continue
		}
		for _, c := range customers {
			if c.Phone == "" || seen[c.Phone+e.ID] {
				continue
			}
			match := false
			for _, at := range e.Attendees {
				match = match || (c.Email != "" && strings.EqualFold(at, c.Email))
			}
			if !match && len(c.Name) >= 4 {
				match = strings.Contains(core.Norm(e.Summary), core.Norm(c.Name))
			}
			if match {
				seen[c.Phone+e.ID] = true
				list = append(list, reminder{Phone: c.Phone, Name: orName(&c), At: e.Start.Format(time.RFC3339), Title: e.Summary})
			}
		}
		if len(list) >= 30 {
			break
		}
	}
	if len(list) == 0 {
		return nil, nil
	}
	business := a.store.TenantName(ctx)
	var detail strings.Builder
	for _, r := range list {
		at, _ := time.Parse(time.RFC3339, r.At)
		fmt.Fprintf(&detail, "· %s %s (%s)\n", at.In(a.loc).Format("15:04"), r.Name, r.Title)
	}
	detail.WriteString("\nLes mando:\n" + a.reminderText(list[0], business))
	raw, _ := json.Marshal(list)
	ap := store.Approval{Area: "Agenda", Risk: "bajo", Title: "Mandar recordatorio a " + plural(len(list), "turno de mañana", "turnos de mañana"),
		Detail: detail.String(), Action: store.Action{Type: "reminders.send", Text: string(raw)}}
	id, err := a.store.CreateApproval(ctx, ap)
	if err != nil {
		return nil, err
	}
	ap.ID = id
	return &ap, nil
}

func (a *Agent) sendReminders(ctx context.Context, ap store.Approval, by string) string {
	if a.remind == nil {
		return "Los recordatorios no están activados."
	}
	var list []reminder
	if json.Unmarshal([]byte(ap.Action.Text), &list) != nil {
		return "La lista estaba rota, no mandé nada."
	}
	business := a.store.TenantName(ctx)
	sent, closed := 0, []string{}
	for _, r := range list {
		at, _ := time.Parse(time.RFC3339, r.At)
		at = at.In(a.loc)
		err := a.remind.Notify(ctx, r.Phone, a.reminderText(r, business), a.remindTpl, a.remindLang, strings.Fields(r.Name + " ")[0], longDate(at), at.Format("15:04"))
		if err != nil {
			closed = append(closed, r.Name)
			continue
		}
		sent++
	}
	_ = a.store.Log(ctx, store.LogEntry{Area: "Agenda", Tool: "reminders.send", Summary: fmt.Sprintf("Recordó %s de mañana", plural(sent, "turno", "turnos")), Via: "code", ApprovedBy: by})
	msg := "Listo, mandé " + plural(sent, "recordatorio", "recordatorios") + "."
	if len(closed) > 0 {
		msg += " No les pude escribir a " + strings.Join(closed, ", ") + " (ventana de 24 h cerrada y sin plantilla de recordatorio)."
	}
	return msg
}

func firstName(name string) string {
	if f := strings.Fields(name); len(f) > 0 && !strings.HasPrefix(f[0], "+") {
		return f[0]
	}
	return "¿cómo estás?"
}
