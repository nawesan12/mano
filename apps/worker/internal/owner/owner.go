// Package owner atiende al dueño, sea por Telegram o por WhatsApp.
// Cada canal traduce su entrada a estas llamadas y su salida a Channel.
package owner

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/nawesan12/mano/worker/internal/agent"
	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/mercadopago"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/stt"
)

// Channel es por dónde se le contesta al dueño en este turno.
type Channel interface {
	Send(ctx context.Context, text string) error
	ApprovalCard(ctx context.Context, a store.Approval) error
}

// Dialer llama al dueño a pedido (voice.Caller). nil si la voz está apagada.
type Dialer interface {
	CallNow(ctx context.Context) string
}

type Service struct {
	Agent  *agent.Agent
	Models *agent.Models
	Store  *store.Store
	STT    *stt.Client
	Calls  Dialer
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

var holderRe = regexp.MustCompile(`(?i)\s+titular\s+(.+?)\s+dni\s+(\d[\d.]{6,10})\s*$`)

var dniRe = regexp.MustCompile(`(?i)\s*\bdni\s+(\d[\d.]{6,10})`)

// ParseInvoice: "150000 Juan Pérez dni 12.345.678: diseño de logo" → monto, cliente, dni, concepto.
func ParseInvoice(args string) (float64, string, string, string, bool) {
	head, concept, _ := strings.Cut(args, ":")
	dni := ""
	if m := dniRe.FindStringSubmatch(head); m != nil {
		dni = strings.ReplaceAll(m[1], ".", "")
		head = dniRe.ReplaceAllString(head, "")
	}
	f := strings.Fields(head)
	if len(f) == 0 {
		return 0, "", "", "", false
	}
	amount, ok := mercadopago.ParseAmount(f[0])
	if !ok {
		return 0, "", "", "", false
	}
	return amount, strings.Join(f[1:], " "), dni, strings.TrimSpace(concept), true
}

// ParseTransfer: "<mail o CBU> <monto> <motivo> [titular Nombre Apellido dni 12345678]".
func ParseTransfer(args string) (mercadopago.Destination, float64, string, string) {
	const usage = "Usalo así: /transferir proveedor@mail.com 50000 factura 88, o /transferir <CBU> 50000 factura 88 titular Juan Pérez dni 12345678"
	var d mercadopago.Destination
	if m := holderRe.FindStringSubmatchIndex(args); m != nil {
		d.Holder = strings.TrimSpace(args[m[2]:m[3]])
		d.DNI = strings.ReplaceAll(args[m[4]:m[5]], ".", "")
		args = args[:m[0]]
	}
	f := strings.Fields(args)
	if len(f) < 3 {
		return d, 0, "", usage
	}
	amount, ok := mercadopago.ParseAmount(f[1])
	if !ok {
		return d, 0, "", usage
	}
	dest := strings.ReplaceAll(f[0], "-", "")
	if strings.Contains(dest, "@") {
		d.Email = dest
	} else {
		d.CBU = dest
	}
	return d, amount, strings.Join(f[2:], " "), ""
}

// chargeRe: "Pérez 150.000 factura 123" → quién, monto, concepto
var chargeRe = regexp.MustCompile(`^(.+?)\s+\$?(\d[\d.,]*[kK]?)\s*(.*)$`)

var factRe = regexp.MustCompile(`(?s)^([^:]{1,60}):\s*(.+)$`)

const help = "Comandos: /resumen /pendientes /gasto /recordar Tema: dato /olvidar Tema /regla (en tus palabras) /llamar Quién: para qué /cobrar Quién monto concepto /cobros /devolver pago /transferir destino monto motivo /envios /ausente hasta el lunes /volvi /diagnostico /cliente Quién /nota Quién: dato /facturar monto cliente: concepto /llamame /silencio 21-8 /llamadas 3. O escribime normal."

// HandleText atiende un mensaje de texto del dueño: comandos primero (gratis), después el agente.
func (s *Service) HandleText(ctx context.Context, ch Channel, text string) {
	say := func(t string) { _ = ch.Send(ctx, t) }
	cmd, args, isCmd := ParseCommand(text)
	if !isCmd {
		if strings.TrimSpace(text) == "" {
			return
		}
		// "cobrale 150 lucas a Pérez" → /cobrar … : mismas validaciones y la misma tarjeta para aprobar
		if c, ok := s.Agent.Route(ctx, text); ok {
			s.HandleText(ctx, &understood{Channel: ch, cmd: c}, c)
			return
		}
		say(s.Agent.OnOwnerMessage(ctx, text))
		return
	}
	switch cmd {
	case "start":
		say(s.Agent.Welcome(ctx, help))
	case "ayuda", "help":
		say(help)
	case "negocio", "rubro":
		if args == "" {
			say("Usalo así: /negocio Costa Azul, inmobiliaria. Rubros: " + core.PlaybookNames())
			return
		}
		name, vertical := args, args
		if i := strings.LastIndex(args, ","); i >= 0 {
			name, vertical = strings.TrimSpace(args[:i]), strings.TrimSpace(args[i+1:])
		}
		if cmd == "rubro" {
			name = ""
		}
		say(s.Agent.SetupBusiness(ctx, name, vertical))
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
	case "regla":
		if args == "" {
			say("Decime la regla en tus palabras. Por ejemplo: /regla llamame si escribe Liliana o si entra un pago de más de 500.000")
			return
		}
		msg, ap := s.Agent.ProposeRule(ctx, args)
		if ap == nil {
			say(msg)
			return
		}
		_ = ch.ApprovalCard(ctx, *ap)
	case "llamar":
		m := factRe.FindStringSubmatch(args)
		if m == nil {
			say("Usalo así: /llamar Pérez: recordarle que vence la factura 123. Te muestro la llamada y sale solo si la aprobás.")
			return
		}
		msg, ap := s.Agent.ProposeCustomerCall(ctx, strings.TrimSpace(m[1]), strings.TrimSpace(m[2]))
		if ap == nil {
			say(msg)
			return
		}
		_ = ch.ApprovalCard(ctx, *ap)
	case "cobrar":
		m := chargeRe.FindStringSubmatch(args)
		amount, ok := 0.0, m != nil
		if ok {
			amount, ok = mercadopago.ParseAmount(m[2])
		}
		if !ok {
			say("Usalo así: /cobrar Pérez 150000 factura 123. Armo el link y se lo mando solo si lo aprobás.")
			return
		}
		msg, ap := s.Agent.ProposeCharge(ctx, strings.TrimSpace(m[1]), amount, m[3])
		if ap == nil {
			say(msg)
			return
		}
		_ = ch.ApprovalCard(ctx, *ap)
	case "devolver":
		f := strings.Fields(args)
		if len(f) == 0 {
			say("Usalo así: /devolver <número de pago> [monto]. Sin monto, devuelvo el total.")
			return
		}
		amount := 0.0
		if len(f) > 1 {
			amount, _ = mercadopago.ParseAmount(f[1])
		}
		msg, ap := s.Agent.ProposeRefund(ctx, f[0], amount)
		if ap == nil {
			say(msg)
			return
		}
		_ = ch.ApprovalCard(ctx, *ap)
	case "transferir":
		d, amount, reason, problem := ParseTransfer(args)
		if problem != "" {
			say(problem)
			return
		}
		msg, ap := s.Agent.ProposePayout(ctx, d, amount, reason)
		if ap == nil {
			say(msg)
			return
		}
		_ = ch.ApprovalCard(ctx, *ap)
	case "facturar":
		amount, who, dni, concept, ok := ParseInvoice(args)
		if !ok {
			say("Usalo así: /facturar 150000 Juan Pérez: diseño de logo (sumá dni 12345678 si hace falta)")
			return
		}
		msg, ap := s.Agent.ProposeInvoice(ctx, amount, who, dni, concept, "")
		if ap == nil {
			say(msg)
			return
		}
		_ = ch.ApprovalCard(ctx, *ap)
	case "cliente", "ficha":
		if args == "" {
			say("Usalo así: /cliente Pérez (o el teléfono o el mail)")
			return
		}
		say(s.Agent.CustomerCard(ctx, args))
	case "nota":
		m := factRe.FindStringSubmatch(args)
		if m == nil {
			say("Usalo así: /nota Pérez: prefiere que lo llamen a la tarde")
			return
		}
		say(s.Agent.AddNote(ctx, strings.TrimSpace(m[1]), strings.TrimSpace(m[2])))
	case "diagnostico", "diagnóstico", "semana":
		t, err := s.Agent.WeeklyReport(ctx)
		if err != nil {
			t = "No pude armar el diagnóstico: " + err.Error()
		}
		say(t)
	case "ausente", "vacaciones":
		if args == "" {
			say("Usalo así: /ausente hasta el lunes, o /ausente 20/10: mensaje para los clientes")
			return
		}
		when, reply, _ := strings.Cut(args, ":")
		msg, ap := s.Agent.StartAway(ctx, when, reply)
		say(msg)
		if ap != nil {
			_ = ch.ApprovalCard(ctx, *ap)
		}
	case "volvi", "volví":
		if m := s.Agent.EndAway(ctx); m != "" {
			say(m)
		} else {
			say("No estabas en modo ausencia.")
		}
	case "envios", "envíos":
		say(s.shipments(ctx))
	case "cobros", "saldo", "movimientos":
		say(s.Agent.Movements(ctx))
	case "llamame":
		if s.Calls == nil {
			say("Las llamadas no están activadas. Configurá la voz en el .env (VOICE_ENABLED).")
			return
		}
		say(s.Calls.CallNow(ctx))
	case "silencio", "llamadas":
		s.callPolicy(ctx, cmd, args, say)
	default:
		say("No conozco ese comando. " + help)
	}
}

var shipStatus = map[string]string{"pending": "pendiente", "handling": "en preparación", "ready_to_ship": "para despachar",
	"shipped": "en camino", "not_delivered": "no entregado"}

// shipments lista los envíos de Mercado Libre que todavía no terminaron. Sin IA.
func (s *Service) shipments(ctx context.Context) string {
	open, err := s.Store.OpenShipments(ctx)
	if err != nil {
		return "No pude leer los envíos: " + err.Error()
	}
	if len(open) == 0 {
		return "No hay envíos en curso."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Envíos en curso: %d", len(open))
	for i, sh := range open {
		if i == 10 {
			fmt.Fprintf(&b, "\n· y %d más", len(open)-10)
			break
		}
		st := shipStatus[sh.Status]
		if st == "" {
			st = sh.Status
		}
		fmt.Fprintf(&b, "\n· %d %s", sh.ID, st)
		if sh.Receiver != "" {
			b.WriteString(" · " + sh.Receiver)
		}
		if sh.Tracking != "" {
			b.WriteString(" · seguimiento " + sh.Tracking)
		}
	}
	return b.String()
}

// callPolicy ajusta cuándo y cuánto puede llamar Mano. Sin argumentos, muestra cómo está.
func (s *Service) callPolicy(ctx context.Context, cmd, args string, say func(string)) {
	p, err := s.Store.CallPolicy(ctx)
	if err != nil {
		say("No pude leer la configuración de llamadas: " + err.Error())
		return
	}
	args = strings.ToLower(strings.TrimSpace(args))
	switch {
	case args == "":
		state := "activas"
		if !p.Enabled {
			state = "apagadas"
		}
		say(fmt.Sprintf("Llamadas %s. Silencio de %s a %s. Hasta %d por día.", state, p.QuietFrom, p.QuietTo, p.MaxPerDay))
		return
	case cmd == "llamadas" && (args == "off" || args == "no" || args == "0"):
		p.Enabled = false
	case cmd == "llamadas" && (args == "on" || args == "si" || args == "sí"):
		p.Enabled = true
	case cmd == "llamadas":
		n, err := strconv.Atoi(args)
		if err != nil || n < 1 || n > 20 {
			say("Usalo así: /llamadas 3 (máximo por día), /llamadas off o /llamadas on")
			return
		}
		p.MaxPerDay, p.Enabled = n, true
	case cmd == "silencio":
		from, to, err := core.ParseQuiet(args)
		if err != nil {
			say(err.Error())
			return
		}
		p.QuietFrom, p.QuietTo = from, to
	}
	if err := s.Store.SetCallPolicy(ctx, p); err != nil {
		say("No pude guardarlo: " + err.Error())
		return
	}
	if !p.Enabled {
		say("Listo, no te llamo más. Te aviso por mensaje. Para volver: /llamadas on")
		return
	}
	say(fmt.Sprintf("Listo. Te llamo solo por lo crítico, de %s a %s, hasta %d por día.", p.QuietTo, p.QuietFrom, p.MaxPerDay))
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
	heard := "Entendí: \"" + core.Truncate(text, 200) + "\""
	if c, ok := s.Agent.Route(ctx, text); ok {
		s.HandleText(ctx, &understood{Channel: ch, cmd: c, heard: heard}, c)
		return
	}
	_ = ch.Send(ctx, heard+"\n\n"+s.Agent.OnOwnerMessage(ctx, text))
}

// understood antepone lo que se entendió a la respuesta (en el mismo mensaje: un turno, un mensaje).
// Las tarjetas no lo necesitan: ya muestran qué se va a hacer.
type understood struct {
	Channel
	cmd, heard string // heard: lo transcripto, si vino por audio
	done       bool
}

func (u *understood) Send(ctx context.Context, text string) error {
	if !u.done {
		u.done = true
		prefix := "Entendí: " + u.cmd
		if u.heard != "" {
			prefix = u.heard + " → " + u.cmd
		}
		text = prefix + "\n" + text
	}
	return u.Channel.Send(ctx, text)
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

// VoiceSender es un canal que manda notas de voz.
type VoiceSender interface {
	SendVoice(ctx context.Context, audio []byte, caption string) error
}

// SendVoice manda la nota por el primer canal que pueda; si ninguno manda audio, va el texto.
func (f Fallback) SendVoice(ctx context.Context, audio []byte, caption string) error {
	for _, c := range f {
		if v, ok := c.(VoiceSender); ok && v.SendVoice(ctx, audio, caption) == nil {
			return nil
		}
	}
	return f.Send(ctx, caption)
}

var errNoChannel = fmt.Errorf("no hay canal configurado para avisarle al dueño")
