package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/nawesan12/mano/worker/internal/core"
)

// Welcome es lo que dice Mano en /start: si todavía no conoce el negocio, lo pregunta.
func (a *Agent) Welcome(ctx context.Context, help string) string {
	name, vertical, _ := a.store.Business(ctx)
	if vertical == "" {
		return "Hola, soy Mano. Para arrancar contame cómo se llama tu negocio y a qué se dedica, por ejemplo:\n" +
			"/negocio Costa Azul, inmobiliaria\nRubros que ya conozco: " + core.PlaybookNames() + ". O escribímelo con tus palabras."
	}
	return fmt.Sprintf("Hola. Ya estoy trabajando para %s. %s", name, help)
}

// SetupBusiness aplica el playbook del rubro. text: "Costa Azul, inmobiliaria" o "tengo una inmobiliaria que se llama Costa Azul".
func (a *Agent) SetupBusiness(ctx context.Context, name, vertical string) string {
	p := core.FindPlaybook(vertical)
	if p == nil {
		p = core.FindPlaybook(name)
	}
	if p == nil {
		return "No conozco ese rubro todavía. Los que tengo: " + core.PlaybookNames() + ". Elegí el más parecido, por ejemplo: /negocio Costa Azul, servicios"
	}
	if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(vertical)) {
		name = ""
	}
	if err := a.store.ApplyPlaybook(ctx, *p, strings.TrimSpace(name)); err != nil {
		return "No pude configurarlo: " + err.Error()
	}
	who := strings.TrimSpace(name)
	if who == "" {
		who, _, _ = a.store.Business(ctx)
	}
	var notes []string
	for _, r := range p.Rules {
		notes = append(notes, r.Outcome.Note)
	}
	var goals []string
	for _, m := range p.Missions {
		goals = append(goals, m[1])
	}
	var ask strings.Builder
	for _, q := range p.Ask {
		ask.WriteString("\n/recordar " + q)
	}
	return fmt.Sprintf("Listo: %s, %s. Ya reconozco sin gastar IA: %s.\nMis misiones: %s.\nContame lo que más sirva que sepa (cambiá los datos por los tuyos):%s",
		who, strings.ToLower(p.Name), strings.ToLower(strings.Join(notes, "; ")), strings.Join(goals, "; "), ask.String())
}
