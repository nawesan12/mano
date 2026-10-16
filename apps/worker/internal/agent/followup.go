package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/store"
)

// RemindAfter es cuánto espera un pendiente antes de recordárselo al dueño (una sola vez).
const RemindAfter = 4 * time.Hour

// FollowUp recuerda lo que espera tu ok y vence lo viejo. Sin IA. Devuelve el mensaje para el dueño ("" = nada que decir).
// No escribe en horario de silencio: lo que se junte sale en el próximo turno.
func (a *Agent) FollowUp(ctx context.Context, now time.Time) (string, error) {
	pol, _ := a.store.CallPolicy(ctx)
	if pol.InQuiet(now.In(a.loc)) {
		return "", nil
	}
	// La ausencia vence sola: al volver, el resumen de lo que pasó.
	if aw, _ := a.store.GetAway(ctx); aw != nil && now.After(aw.Until) {
		return a.EndAway(ctx), nil
	}
	if a.away(ctx) != nil {
		return "", nil // ausente: nada de recordatorios, todo espera al resumen
	}
	open, err := a.store.OpenApprovals(ctx)
	if err != nil {
		return "", err
	}
	var remind []store.OpenApproval
	var expired []string
	for _, ap := range open {
		switch {
		case ap.ExpiresAt != nil && now.After(*ap.ExpiresAt):
			ok, err := a.store.ExpireApproval(ctx, ap.ID)
			if err != nil || !ok {
				continue // se decidió en el medio
			}
			_ = a.store.SetTask(ctx, ap.TaskID, "escalated", map[string]any{"result": map[string]string{"error": "venció sin respuesta"}})
			_ = a.store.Log(ctx, store.LogEntry{Area: ap.Area, Tool: "approval", Summary: "Venció sin respuesta: " + ap.Title, Via: "code", TaskID: ap.TaskID})
			expired = append(expired, ap.Title)
		case ap.RemindedAt == nil && !ap.CreatedAt.IsZero() && now.Sub(ap.CreatedAt) >= RemindAfter:
			remind = append(remind, ap)
		}
	}

	var msg []string
	if len(remind) > 0 {
		oldest := remind[0]
		line := fmt.Sprintf("Sigue esperando tu ok: %s (hace %s)", oldest.Title, ago(now.Sub(oldest.CreatedAt)))
		if len(remind) > 1 {
			line += fmt.Sprintf(" y %d más", len(remind)-1)
		}
		msg = append(msg, line+". Escribí /pendientes para verlas.")
		ids := make([]string, len(remind))
		for i, r := range remind {
			ids[i] = r.ID
		}
		if err := a.store.MarkReminded(ctx, ids, now); err != nil {
			return "", err
		}
	}
	if len(expired) > 0 {
		msg = append(msg, "Venció sin respuesta, no hice nada: "+strings.Join(expired, "; ")+".")
	}
	if a.pay != nil {
		msg = append(msg, a.chargeFollowUp(ctx, now)...)
	}
	msg = append(msg, a.CheckPayouts(ctx)...)
	return strings.Join(msg, "\n"), nil
}

func ago(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d días", int(d.Hours()/24))
}

// Closing es el cierre del día: qué hizo Mano, qué queda y cuánto costó. Sin IA.
func (a *Agent) Closing(ctx context.Context) (string, error) {
	done, err := a.store.TodayLog(ctx, a.loc)
	if err != nil {
		return "", err
	}
	pending, _ := a.store.PendingApprovals(ctx)
	sp, caps, _ := a.models.Status(ctx)

	var b strings.Builder
	if len(done) == 0 {
		b.WriteString("Cierre del día. Hoy no hice nada que necesitara tu ok.")
	} else {
		fmt.Fprintf(&b, "Cierre del día. Hice %d cosa(s):", len(done))
		for i, d := range done {
			if i == 5 {
				fmt.Fprintf(&b, "\n· y %d más en el panel", len(done)-5)
				break
			}
			b.WriteString("\n· " + d.Summary)
		}
	}
	if len(pending) > 0 {
		fmt.Fprintf(&b, "\nQuedan %d esperando tu ok (/pendientes).", len(pending))
	}
	fmt.Fprintf(&b, "\nGasto de IA hoy: USD %.2f de %.0f.", sp.DayUSD, caps.DayUSD)
	return b.String(), nil
}
