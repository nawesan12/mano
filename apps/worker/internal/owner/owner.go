// Package owner atiende al dueño, sea por Telegram o por WhatsApp.
// Cada canal traduce su entrada a estas llamadas y su salida a Channel.
package owner

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/nawesan12/mano/worker/internal/agent"
	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/stt"
)

// Channel es por dónde se le contesta al dueño en este turno.
type Channel interface {
	Send(ctx context.Context, text string) error
	ApprovalCard(ctx context.Context, a store.Approval) error
}

type Service struct {
	Agent  *agent.Agent
	Models *agent.Models
	Store  *store.Store
	STT    *stt.Client
}

// ParseCommand: "/recordar@ManoBot Tema: dato" → ("recordar", "Tema: dato")
func ParseCommand(text string) (cmd, args string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, _, _ = strings.Cut(head, "@")
	return strings.ToLower(head), strings.TrimSpace(rest), true
}

// DecisionRe es el contrato de los botones de aprobación: ap:<uuid>:ok|no|ig
var DecisionRe = regexp.MustCompile(`^ap:([0-9a-f-]{36}):(ok|no|ig)$`)

var factRe = regexp.MustCompile(`(?s)^([^:]{1,60}):\s*(.+)$`)

const help = "Comandos: /resumen /pendientes /gasto /recordar Tema: dato /olvidar Tema. O escribime normal."

// HandleText atiende un mensaje de texto del dueño: comandos primero (gratis), después el agente.
func (s *Service) HandleText(ctx context.Context, ch Channel, text string) {
	say := func(t string) { _ = ch.Send(ctx, t) }
	cmd, args, isCmd := ParseCommand(text)
	if !isCmd {
		if strings.TrimSpace(text) != "" {
			say(s.Agent.OnOwnerMessage(ctx, text))
		}
		return
	}
	switch cmd {
	case "start", "ayuda", "help":
		say("Hola. Ya estoy trabajando. " + help)
	case "resumen":
		t, err := s.Agent.Brief(ctx)
		if err != nil {
			t = "No pude armar el resumen: " + err.Error()
		}
		say(t)
	case "pendientes":
		p, err := s.Store.PendingApprovals(ctx)
		if err != nil || len(p) == 0 {
			say("No hay nada esperando tu ok.")
			return
		}
		for _, a := range p {
			_ = ch.ApprovalCard(ctx, a)
		}
	case "gasto":
		sp, caps, _ := s.Models.Status(ctx)
		say(fmt.Sprintf("Hoy USD %.3f de %.2f\nMes USD %.2f de %.2f\nÚltima hora USD %.3f", sp.DayUSD, caps.DayUSD, sp.MonthUSD, caps.MonthUSD, sp.HourUSD))
	case "recordar":
		m := factRe.FindStringSubmatch(args)
		if m == nil {
			say("Usalo así: /recordar Tema: dato. Por ejemplo: /recordar Honorarios: 3% más IVA")
			return
		}
		if err := s.Store.AddFact(ctx, strings.TrimSpace(m[1]), strings.TrimSpace(m[2])); err != nil {
			say("No pude guardarlo: " + err.Error())
			return
		}
		say("Anotado: " + strings.TrimSpace(m[1]) + ".")
	case "olvidar":
		if args == "" {
			say("Usalo así: /olvidar Tema")
			return
		}
		n, err := s.Store.ForgetTopic(ctx, args)
		switch {
		case err != nil:
			say("No pude borrarlo: " + err.Error())
		case n == 0:
			say("No tenía nada guardado sobre " + args + ".")
		default:
			say(fmt.Sprintf("Olvidé %d dato(s) sobre %s.", n, args))
		}
	default:
		say("No conozco ese comando. " + help)
	}
}

// HandleButton resuelve el id de un botón. Devuelve false si no es un botón de Mano.
func (s *Service) HandleButton(ctx context.Context, ch Channel, id, by string) bool {
	m := DecisionRe.FindStringSubmatch(id)
	if m == nil {
		return false
	}
	d := map[string]agent.Decision{"ok": agent.Approve, "no": agent.Reject, "ig": agent.IgnoreSender}[m[2]]
	_ = ch.Send(ctx, s.Agent.OnDecision(ctx, m[1], d, by))
	return true
}

// HandleAudio transcribe y lo trata como texto. Un turno = un mensaje: el "Entendí" va en la misma respuesta.
func (s *Service) HandleAudio(ctx context.Context, ch Channel, audio []byte, filename string) {
	text, err := s.STT.Transcribe(ctx, audio, filename)
	if err != nil {
		_ = ch.Send(ctx, "No pude escuchar el audio ("+err.Error()+"). Escribime por ahora.")
		return
	}
	if text == "" {
		_ = ch.Send(ctx, "El audio llegó vacío. ¿Me lo repetís?")
		return
	}
	if _, _, isCmd := ParseCommand(text); isCmd {
		s.HandleText(ctx, ch, text)
		return
	}
	_ = ch.Send(ctx, "Entendí: \""+core.Truncate(text, 200)+"\"\n\n"+s.Agent.OnOwnerMessage(ctx, text))
}

// Fallback avisa por el primer canal que funcione (por ejemplo WhatsApp con la ventana
// cerrada y sin plantilla → Telegram). Implementa Channel y agent.Notifier.
type Fallback []Channel

func (f Fallback) Send(ctx context.Context, text string) error {
	var last error = errNoChannel
	for _, c := range f {
		if last = c.Send(ctx, text); last == nil {
			return nil
		}
	}
	return last
}

func (f Fallback) ApprovalCard(ctx context.Context, a store.Approval) error {
	var last error = errNoChannel
	for _, c := range f {
		if last = c.ApprovalCard(ctx, a); last == nil {
			return nil
		}
	}
	return last
}

var errNoChannel = fmt.Errorf("no hay canal configurado para avisarle al dueño")
