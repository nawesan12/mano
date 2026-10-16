package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

// Scheduler crea eventos en el calendario (calendar.Client).
type Scheduler interface {
	CreateEvent(ctx context.Context, title string, start, end time.Time, attendees []string, description string) (string, error)
}

// Horario laboral para buscar huecos: de 9 a 18 salvo que el dueño lo cambie.
const workFrom, workTo = 9, 18

func (a *Agent) SetScheduler(s Scheduler) { a.scheduler = s }

func (a *Agent) busyOn(ctx context.Context, day time.Time) []core.Slot {
	evs, _ := a.store.CalendarEvents(ctx)
	next := day.Add(24 * time.Hour)
	var out []core.Slot
	for _, e := range evs {
		if e.Status == "cancelled" || !e.Busy || e.AllDay || !e.Start.Before(next) || !e.End.After(day) {
			continue
		}
		out = append(out, core.Slot{From: e.Start.In(a.loc), To: e.End.In(a.loc)})
	}
	return out
}

// Free contesta "¿cuándo tengo libre el jueves?" con la copia local del calendario. Sin IA.
func (a *Agent) Free(ctx context.Context, dayText string) string {
	day, err := core.ParseDay(dayText, time.Now(), a.loc)
	if err != nil {
		return "No entendí el día: " + err.Error()
	}
	slots := core.FreeSlots(day, a.busyOn(ctx, day), workFrom, workTo, 30*time.Minute)
	if day.Before(time.Now()) { // hoy: desde ahora
		now := time.Now().In(a.loc)
		kept := slots[:0]
		for _, s := range slots {
			if s.To.After(now.Add(30 * time.Minute)) {
				if s.From.Before(now) {
					s.From = now.Truncate(15 * time.Minute).Add(15 * time.Minute)
				}
				kept = append(kept, s)
			}
		}
		slots = kept
	}
	label := daysES[day.Weekday()] + " " + fmt.Sprint(day.Day())
	if len(slots) == 0 {
		return "El " + label + " no tenés huecos de media hora entre las 9 y las 18."
	}
	var parts []string
	for _, s := range slots {
		parts = append(parts, s.From.Format("15:04")+" a "+s.To.Format("15:04"))
	}
	return "El " + label + " tenés libre: " + strings.Join(parts, ", ") + "."
}

// ProposeMeeting deja para aprobar un evento. Si choca con algo, lo dice; si el invitado está en el CRM, lo invita.
func (a *Agent) ProposeMeeting(ctx context.Context, whenText string, minutes int, title, with string) (string, *store.Approval) {
	if a.scheduler == nil {
		return "Para agendar necesito Google Calendar conectado (CALENDAR_ENABLED y el permiso calendar.events).", nil
	}
	start, err := core.ParseWhen(whenText, time.Now(), a.loc)
	if err != nil {
		return "No lo agendé: " + err.Error() + ".", nil
	}
	if minutes <= 0 || minutes > 8*60 {
		minutes = 60
	}
	end := start.Add(time.Duration(minutes) * time.Minute)
	email, who := "", strings.TrimSpace(with)
	if who != "" {
		if c, _ := a.findCustomer(ctx, who); c != nil {
			who, email = orName(c), c.Email
		}
	}
	if title = strings.TrimSpace(title); title == "" {
		title = "Reunión"
		if who != "" {
			title += " con " + who
		}
	}
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, a.loc)
	detail := fmt.Sprintf("%s, %s a %s.", longDate(start), start.Format("15:04"), end.Format("15:04"))
	for _, b := range a.busyOn(ctx, day) {
		if b.From.Before(end) && start.Before(b.To) {
			detail += fmt.Sprintf(" Ojo: se pisa con algo de %s a %s.", b.From.Format("15:04"), b.To.Format("15:04"))
			break
		}
	}
	if email != "" {
		detail += " Le mando la invitación a " + email + "."
	} else if who != "" {
		detail += " No tengo el mail de " + who + ": queda solo en tu agenda."
	}
	ap := store.Approval{Area: "Agenda", Risk: "bajo", Title: core.Truncate(title+" · "+start.Format("02/01 15:04"), 90), Detail: detail,
		Action: store.Action{Type: "calendar.create", Text: core.Truncate(title, 120), Ref: start.Format(time.RFC3339), Amount: float64(minutes), To: email}}
	id, err := a.store.CreateApproval(ctx, ap)
	if err != nil {
		return "No pude dejarlo para que lo apruebes: " + err.Error(), nil
	}
	ap.ID = id
	return "", &ap
}

func (a *Agent) createMeeting(ctx context.Context, ap store.Approval, by string) string {
	if a.scheduler == nil {
		return "Google Calendar no está conectado."
	}
	start, err := time.Parse(time.RFC3339, ap.Action.Ref)
	if err != nil {
		return "La fecha estaba rota, no agendé nada."
	}
	end := start.Add(time.Duration(ap.Action.Amount) * time.Minute)
	var attendees []string
	if ap.Action.To != "" {
		attendees = []string{ap.Action.To}
	}
	link, err := a.scheduler.CreateEvent(ctx, ap.Action.Text, start, end, attendees, "Agendado por Mano.")
	if err != nil {
		return "No pude agendarlo: " + err.Error()
	}
	_ = a.store.Log(ctx, store.LogEntry{Area: "Agenda", Tool: "calendar.create", Summary: "Agendó " + ap.Action.Text + " el " + start.In(a.loc).Format("02/01 15:04"), Via: "code", ApprovedBy: by})
	msg := "Agendado: " + ap.Action.Text + ", " + longDate(start.In(a.loc)) + " a las " + start.In(a.loc).Format("15:04") + "."
	if len(attendees) > 0 {
		msg += " Invitación enviada."
	}
	if link != "" {
		msg += "\n" + link
	}
	return msg
}
