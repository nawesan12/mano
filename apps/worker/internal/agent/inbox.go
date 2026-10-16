package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

// Archiver saca mails del inbox y los devuelve (gmail.Client). Reversible: nunca borra.
type Archiver interface {
	Archive(ctx context.Context, id string) error
	Unarchive(ctx context.Context, id string) error
}

// SetArchiver activa el inbox limpio: lo que se ignora (newsletters, automáticos) se archiva solo.
func (a *Agent) SetArchiver(ar Archiver) { a.archiver = ar }

// archiveIgnored: nivel 2 de autonomía, hace lo reversible y avisa (en el resumen).
func (a *Agent) archiveIgnored(ctx context.Context, ev core.Event, t core.Triage) {
	if a.archiver == nil || ev.Source != "gmail" || t.Urgency != core.Ignore {
		return
	}
	if err := a.archiver.Archive(ctx, ev.ExternalID); err != nil {
		return
	}
	_ = a.store.AddArchived(ctx, store.Archived{MessageID: ev.ExternalID, Subject: core.Truncate(ev.Subject, 200), Sender: core.Truncate(ev.Sender, 200)})
}

// archivedLine es la línea del resumen: cuánto se archivó en el último día.
func (a *Agent) archivedLine(ctx context.Context) string {
	if a.archiver == nil {
		return ""
	}
	rows, err := a.store.ArchivedSince(ctx, time.Now().Add(-24*time.Hour))
	if err != nil || len(rows) == 0 {
		return ""
	}
	return fmt.Sprintf("Archivé %s (newsletters y automáticos). Están en la etiqueta Mano/Archivado; /desarchivar los devuelve.", plural(len(rows), "mail", "mails"))
}

// Unarchive devuelve al inbox lo archivado en los últimos 7 días (todo, o solo de un remitente).
func (a *Agent) Unarchive(ctx context.Context, sender string) string {
	if a.archiver == nil {
		return "El archivado automático no está activado."
	}
	rows, err := a.store.ArchivedSince(ctx, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		return "No pude leer lo archivado: " + err.Error()
	}
	q := core.Norm(strings.TrimSpace(sender))
	n := 0
	for _, r := range rows {
		if q != "" && !strings.Contains(core.Norm(r.Sender), q) {
			continue
		}
		if a.archiver.Unarchive(ctx, r.MessageID) == nil {
			_ = a.store.MarkRestored(ctx, r.MessageID)
			n++
		}
	}
	switch {
	case n == 0 && q != "":
		return "No archivé nada de " + sender + " esta semana."
	case n == 0:
		return "No hay nada archivado esta semana."
	}
	msg := "Listo, volvieron al inbox " + plural(n, "mail", "mails") + "."
	if q != "" {
		msg += " Si querés que no se archiven más los de " + sender + ", decime: /regla avisame los mails de " + sender
	}
	return msg
}
