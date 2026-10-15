package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/nawesan12/mano/worker/internal/core"
)

// Brief junta lo no urgente en un solo mensaje. Si no hubo nada, se arma sin IA.
func (a *Agent) Brief(ctx context.Context) (string, error) {
	events, err := a.store.Unbriefed(ctx)
	if err != nil {
		return "", err
	}
	pending, _ := a.store.PendingApprovals(ctx)
	sp, caps, _ := a.models.Status(ctx)

	pend := "No hay nada esperando tu ok."
	if len(pending) > 0 {
		titles := make([]string, len(pending))
		for i, p := range pending {
			titles[i] = p.Title
		}
		pend = fmt.Sprintf("Te esperan %d aprobaciones: %s.", len(pending), strings.Join(titles, "; "))
	}
	spend := fmt.Sprintf("Gasto de IA hoy: USD %.2f · mes USD %.2f de %.0f", sp.DayUSD, sp.MonthUSD, caps.MonthUSD)

	if len(events) == 0 {
		return "Buen día. Noche tranquila.\n" + pend + "\n" + spend, nil
	}

	var lines strings.Builder
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
		u := e.Urgency
		if u == "" {
			u = "normal"
		}
		fmt.Fprintf(&lines, "- [%s/%s] %s\n", u, e.Source, e.Summary)
	}

	var body string
	r, err := a.models.Complete(ctx, core.Cheap, core.Request{
		System:    "Sos Mano. Armá el resumen de la mañana para el dueño: máximo 5 viñetas cortas en español rioplatense, lo más importante primero, agrupando lo repetido. Sin saludo ni cierre.",
		Messages:  []core.Message{{Role: "user", Content: core.Truncate(lines.String(), 8000)}},
		MaxTokens: 350,
	}, CallOpts{})
	if err == nil {
		body = strings.TrimSpace(r.Text)
	}
	if body == "" { // frenado por el gobernador o error del modelo: resumen sin IA
		var b strings.Builder
		for i, e := range events {
			if i == 6 {
				break
			}
			b.WriteString("· " + e.Summary + "\n")
		}
		body = strings.TrimSpace(b.String())
	}
	if err := a.store.MarkBriefed(ctx, ids); err != nil {
		return "", err
	}
	return "Buen día. Esto pasó:\n" + body + "\n\n" + pend + "\n" + spend, nil
}
