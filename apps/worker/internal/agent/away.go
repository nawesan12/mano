package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

var monthsES = []string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
var daysES = []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

func longDate(t time.Time) string {
	return fmt.Sprintf("%s %d de %s", daysES[t.Weekday()], t.Day(), monthsES[t.Month()])
}

// StartAway activa el modo ausencia. Mientras dure: solo llama por lo crítico, lo demás va al resumen,
// y a los clientes que escriben les manda una vez la respuesta que el dueño aprobó (sale como tarjeta).
func (a *Agent) StartAway(ctx context.Context, untilText, reply string) (string, *store.Approval) {
	until, err := core.ParseUntil(untilText, time.Now(), a.loc)
	if err != nil {
		return "No lo activé: " + err.Error() + ".", nil
	}
	if reply = strings.TrimSpace(reply); reply == "" {
		who := a.store.TenantName(ctx)
		if who == "" {
			who = "el equipo"
		}
		reply = fmt.Sprintf("Hola, gracias por escribir. %s no está disponible hasta el %s. Te respondemos a la vuelta; si es urgente, contanos y le avisamos.", who, longDate(until))
	}
	if err := a.store.StartAway(ctx, until, core.Truncate(reply, 600)); err != nil {
		return "No pude activarlo: " + err.Error(), nil
	}
	msg := fmt.Sprintf("Listo, quedás ausente hasta el %s. Solo te llamo por lo crítico; lo demás te espera en el resumen. Para volver antes: /volvi", longDate(until))
	if a.wa == nil {
		return msg, nil
	}
	ap := store.Approval{Area: "Ausencia", Risk: "medio", Title: "Responder así a los clientes mientras no estás",
		Detail: "A cada cliente que escriba le mando una vez:\n" + reply, Action: store.Action{Type: "away.reply", Text: reply}}
	id, err := a.store.CreateApproval(ctx, ap)
	if err != nil {
		return msg, nil
	}
	ap.ID = id
	return msg + "\nSi querés que les responda a los clientes, aprobá el mensaje.", &ap
}

func (a *Agent) approveAwayReply(ctx context.Context, ap store.Approval, by string) string {
	if err := a.store.ApproveAwayReply(ctx); err != nil {
		return "No pude guardarlo: " + err.Error()
	}
	_ = a.store.Log(ctx, store.LogEntry{Area: "Ausencia", Tool: "away.reply", Summary: "Aprobaste la respuesta de ausencia", Via: "code", ApprovedBy: by})
	return "Listo: a cada cliente que escriba le respondo eso, una sola vez."
}

// away devuelve la ausencia activa y vigente, o nil.
func (a *Agent) away(ctx context.Context) *store.Away {
	aw, err := a.store.GetAway(ctx)
	if err != nil || aw == nil || !time.Now().Before(aw.Until) {
		return nil
	}
	return aw
}

// awayAutoReply responde una vez por ausencia a un cliente de WhatsApp, con el texto aprobado.
func (a *Agent) awayAutoReply(ctx context.Context, aw *store.Away, ev core.Event) {
	phone, _ := ev.Payload["phone"].(string)
	if ev.Source != "whatsapp" || phone == "" || !aw.Approved || a.wa == nil {
		return
	}
	first, err := a.store.MarkAwayReplied(ctx, phone)
	if err != nil || !first {
		return
	}
	if err := a.wa.ReplyInWindow(ctx, phone, aw.Reply); err == nil {
		_ = a.store.Log(ctx, store.LogEntry{Area: "Ausencia", Tool: "whatsapp.reply", Summary: "Respuesta de ausencia a " + ev.Sender, Via: "code", ApprovedBy: "ausencia"})
	}
}

// EndAway termina la ausencia y arma el resumen de lo que pasó. "" si no había ausencia activa.
func (a *Agent) EndAway(ctx context.Context) string {
	aw, _ := a.store.GetAway(ctx)
	ended, err := a.store.EndAway(ctx)
	if err != nil || !ended || aw == nil {
		return ""
	}
	counts, _ := a.store.EventsSince(ctx, aw.StartedAt)
	replies, _ := a.store.AwayReplies(ctx)
	pending, _ := a.store.PendingApprovals(ctx)
	total := 0
	for _, n := range counts {
		total += n
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Qué bueno que volviste. Mientras no estabas entraron %d cosas: %d críticas, %d importantes, %d para el resumen y %d que ignoré.",
		total, counts["critical"], counts["high"], counts["normal"], counts["ignore"])
	if replies > 0 {
		fmt.Fprintf(&b, "\nLes respondí a %d cliente(s) con tu mensaje de ausencia.", replies)
	}
	if len(pending) > 0 {
		fmt.Fprintf(&b, "\nTe esperan %d aprobaciones: /pendientes", len(pending))
	}
	return b.String()
}
