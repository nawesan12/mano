package voice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nawesan12/mano/worker/internal/agent"
	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/whatsapp"
)

// Phone es la parte de WhatsApp Calling que usa el Caller (whatsapp.Client).
type Phone interface {
	StartCall(ctx context.Context, to, sdpOffer, opaque string) (string, error)
	AnswerCall(ctx context.Context, callID, action, sdpAnswer string) error
	RejectCall(ctx context.Context, callID string) error
	TerminateCall(ctx context.Context, callID string) error
	RequestCallPermission(ctx context.Context, to, body, template, lang string) error
}

// Caller llama al dueño cuando algo es crítico y atiende cuando el dueño llama a Mano.
// Implementa agent.Caller y whatsapp.CallHandler.
type Caller struct {
	Phone    Phone
	Brain    Brain
	NewMedia func() (Media, error)
	Store    *store.Store
	Models   *agent.Models
	Decider  Decider
	Notify   agent.Notifier

	OwnerPhone                       string
	Loc                              *time.Location
	Model, Voice, TranscribeModel    string
	PriceIn, PriceOut                float64 // USD por millón de tokens de la voz
	WAUSDPerMin                      float64 // tarifa de Meta por minuto saliente
	MaxCall, RingTimeout             time.Duration
	HangupDelay                      time.Duration // margen para que termine de despedirse; 0 = 2,5 s
	PermissionTemplate, TemplateLang string

	// After programa un reintento; por defecto time.AfterFunc. Los tests lo reemplazan.
	After func(d time.Duration, f func())
	Now   func() time.Time

	busy atomic.Bool
	live sync.Map // callID → chan signal

	earlyMu sync.Mutex
	early   map[string][]signal // webhooks que llegan antes de que StartCall devuelva el id
}

type signal struct {
	status string // RINGING | ACCEPTED | REJECTED | TERMINATED
	answer string // SDP de respuesta (connect)
	dur    int
}

func (c *Caller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Caller) after(d time.Duration, f func()) {
	if c.After != nil {
		c.After(d, f)
		return
	}
	time.AfterFunc(d, f)
}

// estimate es el costo de IA en el peor caso: la llamada entera al máximo (~600 tokens/min de entrada, ~1200 de salida).
func (c *Caller) estimate() float64 {
	mins := c.MaxCall.Minutes()
	return mins * (600*c.PriceIn + 1200*c.PriceOut) / 1e6
}

func (c *Caller) aiCost(in, out int) float64 {
	return (float64(in)*c.PriceIn + float64(out)*c.PriceOut) / 1e6
}

// waCost: Meta cobra por minuto en tramos de 6 segundos.
func (c *Caller) waCost(seconds int) float64 {
	return math.Ceil(float64(seconds)/6) * 6 / 60 * c.WAUSDPerMin
}

// CallOwner decide (sin IA) si llamar y, si corresponde, llama. Bloquea hasta que la llamada termina.
func (c *Caller) CallOwner(ctx context.Context, r agent.CallRequest) (string, error) {
	if r.Attempt == 0 {
		r.Attempt = 1
	}
	if !c.busy.CompareAndSwap(false, true) {
		return "ya estoy en una llamada", nil
	}
	defer c.busy.Store(false)

	pol, err := c.Store.CallPolicy(ctx)
	if err != nil {
		slog.Warn("voz: política por defecto", "err", err)
	}
	today, missed, err := c.Store.CallStats(ctx, c.Loc)
	if err != nil {
		return "", err
	}
	if d := core.ShouldCall(pol, r.When, c.now().In(c.Loc), today, missed); !d.Call {
		return d.Reason, nil
	}
	critical := r.When.Urgency == core.Critical
	if err := c.Models.AuthorizeVoice(ctx, c.estimate(), critical || r.When.OwnerAsked); err != nil {
		if b, ok := agent.IsBlocked(err); ok {
			return "gasto: " + b.Reason, nil
		}
		return "", err
	}

	media, err := c.NewMedia()
	if err != nil {
		return "", err
	}
	defer media.Close()
	offer, err := media.PhoneOffer(ctx)
	if err != nil {
		return "", err
	}
	sig := make(chan signal, 8)
	callID, err := c.Phone.StartCall(ctx, c.OwnerPhone, offer, r.EventID)
	if errors.Is(err, whatsapp.ErrNoCallPermission) {
		c.askPermission(ctx)
		return "falta el permiso de llamada", nil
	}
	if err != nil {
		return "", err
	}
	c.listen(callID, sig)
	defer c.live.Delete(callID)
	_ = c.Store.InsertCall(ctx, store.Call{ID: callID, Direction: "out", EventID: r.EventID, ApprovalID: r.ApprovalID, Reason: r.Reason, Status: "dialing", Attempt: r.Attempt})

	status := c.ring(ctx, callID, media, sig)
	if status != "accepted" {
		_ = c.Store.FinishCall(ctx, callID, store.CallEnd{Status: status, EndedAt: c.now()})
		c.missed(r, status, pol)
		return "", nil
	}
	c.talk(ctx, callID, "out", c.ownerConvo(ctx, media, r.Reason, r.Detail), media, sig)
	return "", nil
}

// ring espera a que atienda. Devuelve accepted, rejected o missed.
func (c *Caller) ring(ctx context.Context, callID string, media Media, sig chan signal) string {
	timeout := time.NewTimer(c.RingTimeout)
	defer timeout.Stop()
	for {
		select {
		case s := <-sig:
			switch s.status {
			case "CONNECT":
				if err := media.SetPhoneAnswer(s.answer); err != nil {
					slog.Error("voz: sdp de meta", "err", err)
					_ = c.Phone.TerminateCall(ctx, callID)
					return "missed"
				}
			case "RINGING":
				_ = c.Store.UpdateCallStatus(ctx, callID, "ringing")
			case "ACCEPTED":
				_ = c.Store.UpdateCallStatus(ctx, callID, "accepted")
				return "accepted"
			case "REJECTED":
				return "rejected"
			case "TERMINATED":
				return "missed"
			}
		case <-timeout.C:
			_ = c.Phone.TerminateCall(ctx, callID)
			return "missed"
		case <-ctx.Done():
			_ = c.Phone.TerminateCall(context.WithoutCancel(ctx), callID)
			return "missed"
		}
	}
}

// missed: un solo reintento si sigue pendiente; si no, aviso por mensaje. Rechazar es una respuesta: no se insiste.
func (c *Caller) missed(r agent.CallRequest, status string, pol core.CallPolicy) {
	ctx := context.Background()
	if status == "missed" && r.Attempt < pol.MaxAttempts && !r.When.OwnerAsked {
		next := r
		next.Attempt++
		c.after(time.Duration(pol.RetryAfterMin)*time.Minute, func() {
			if next.ApprovalID != "" && !c.stillPending(ctx, next.ApprovalID) {
				return // ya lo resolvió por otro lado
			}
			if why, err := c.CallOwner(ctx, next); err != nil || why != "" {
				slog.Info("voz: reintento sin llamada", "motivo", why, "err", err)
			}
		})
		return
	}
	what := "no atendiste"
	if status == "rejected" {
		what = "cortaste"
	}
	msg := "Te llamé por esto y " + what + ": " + r.Reason
	if r.ApprovalID != "" {
		msg += "\nLo tenés en /pendientes."
	}
	_ = c.Notify.Send(ctx, msg)
}

func (c *Caller) stillPending(ctx context.Context, id string) bool {
	p, err := c.Store.PendingApprovals(ctx)
	if err != nil {
		return true
	}
	for _, a := range p {
		if a.ID == id {
			return true
		}
	}
	return false
}

func (c *Caller) askPermission(ctx context.Context) {
	perm, _ := c.Store.CallPermissionOf(ctx, c.OwnerPhone)
	if perm.RequestedAt != nil && c.now().Sub(*perm.RequestedAt) < 24*time.Hour {
		return // WhatsApp deja un pedido cada 24 h
	}
	err := c.Phone.RequestCallPermission(ctx, c.OwnerPhone, "Cuando algo sea urgente, te quiero llamar por acá. ¿Me das permiso?", c.PermissionTemplate, c.TemplateLang)
	if err != nil {
		_ = c.Notify.Send(ctx, "Quise llamarte pero WhatsApp no me deja: falta tu permiso de llamada. Escribime por WhatsApp y te lo pido.")
		return
	}
	_ = c.Store.MarkPermissionRequested(ctx, c.OwnerPhone, c.now())
}

// session arma la configuración de voz que va con la oferta al modelo.
func (c *Caller) session(instructions string, tools []map[string]any) map[string]any {
	audio := map[string]any{
		"input": map[string]any{"turn_detection": map[string]any{"type": "semantic_vad"}},
	}
	if c.TranscribeModel != "" {
		audio["input"].(map[string]any)["transcription"] = map[string]any{"model": c.TranscribeModel, "language": "es"}
	}
	if c.Voice != "" {
		audio["output"] = map[string]any{"voice": c.Voice}
	}
	return map[string]any{"type": "realtime", "model": c.Model, "instructions": instructions, "audio": audio, "tools": tools, "tool_choice": "auto"}
}

// convo es con quién se habla y qué puede hacer Mano en la llamada.
type convo struct {
	reason       string // para el registro
	instructions string
	tools        []map[string]any
	sess         *session
	approvedBy   string
	report       func(summary string) string // texto al dueño al terminar
	failNote     string
}

// ownerConvo: con el dueño, Mano puede aprobar, descartar, silenciar, recordar y cortar.
func (c *Caller) ownerConvo(ctx context.Context, media Media, reason, detail string) convo {
	facts, _ := c.Store.Facts(ctx)
	missions, _ := c.Store.Missions(ctx)
	pending, _ := c.Store.PendingApprovals(ctx)
	return convo{
		reason: reason, approvedBy: "voz",
		instructions: Instructions(agent.OwnerSystem(facts, missions), reason, detail, pending),
		tools:        Tools(OwnerTools...),
		sess:         newSession(media, c.Decider, c.Store, "voz:"+c.OwnerPhone, pending, OwnerTools),
		report:       func(s string) string { return s },
		failNote:     "Te atendí pero se me cortó la voz. Te dejo el motivo por acá: " + reason,
	}
}

// talk conecta al modelo y corre la conversación hasta que alguno corta.
func (c *Caller) talk(ctx context.Context, callID, direction string, cv convo, media Media, sig chan signal) {
	start := c.now()
	sess := cv.sess

	end := store.CallEnd{Status: "completed", StartedAt: start}
	finish := func() {
		in, out, transcript, done := sess.result()
		end.EndedAt = c.now()
		if end.DurationS == 0 {
			end.DurationS = int(end.EndedAt.Sub(start).Seconds())
		}
		if direction == "out" {
			end.WACostUSD = c.waCost(end.DurationS)
		}
		end.AICostUSD = c.aiCost(in, out)
		end.Transcript, end.Outcome = transcript, done
		end.Summary = summarize(end.DurationS, done)
		if in+out > 0 {
			_ = c.Models.RecordVoice(context.WithoutCancel(ctx), c.Model, in, out, end.AICostUSD)
		}
		_ = c.Store.FinishCall(context.WithoutCancel(ctx), callID, end)
		_ = c.Store.Log(context.WithoutCancel(ctx), store.LogEntry{Area: "Llamadas", Tool: "call", Summary: "Llamada: " + core.Truncate(cv.reason, 120) + ". " + end.Summary,
			Via: "top", Model: c.Model, ApprovedBy: cv.approvedBy, CostUSD: end.AICostUSD + end.WACostUSD})
		if end.Status == "completed" {
			_ = c.Notify.Send(context.WithoutCancel(ctx), cv.report(end.Summary))
		}
	}
	defer finish()

	offer, err := media.BrainOffer(ctx)
	if err == nil {
		var answer string
		if answer, err = c.Brain.Connect(ctx, offer, c.session(cv.instructions, cv.tools)); err == nil {
			err = media.SetBrainAnswer(answer)
		}
	}
	if err != nil {
		slog.Error("voz: no conecté con el modelo", "err", err)
		end.Status = "failed"
		_ = c.Phone.TerminateCall(context.WithoutCancel(ctx), callID)
		_ = c.Notify.Send(context.WithoutCancel(ctx), cv.failNote)
		return
	}
	// El modelo habla primero: saluda y cuenta el motivo.
	_ = media.Send(ctx, map[string]any{"type": "response.create"})

	limit := time.NewTimer(c.MaxCall)
	defer limit.Stop()
	for {
		select {
		case raw := <-media.Events():
			sess.handle(ctx, raw)
		case s := <-sig:
			if s.status == "TERMINATED" {
				if s.dur > 0 {
					end.DurationS = s.dur
				}
				return
			}
		case <-sess.hangup:
			delay := c.HangupDelay
			if delay == 0 {
				delay = 2500 * time.Millisecond
			}
			time.Sleep(delay) // que termine de despedirse
			_ = c.Phone.TerminateCall(ctx, callID)
			return
		case <-limit.C:
			_ = c.Phone.TerminateCall(ctx, callID)
			return
		case <-media.Done():
			_ = c.Phone.TerminateCall(context.WithoutCancel(ctx), callID)
			return
		case <-ctx.Done():
			_ = c.Phone.TerminateCall(context.WithoutCancel(ctx), callID)
			return
		}
	}
}

func summarize(seconds int, done []string) string {
	d := fmt.Sprintf("%d min", int(math.Ceil(float64(seconds)/60)))
	if seconds < 60 {
		d = fmt.Sprintf("%d s", seconds)
	}
	if len(done) == 0 {
		return "Hablamos " + d + ". Sin novedades para anotar."
	}
	return "Hablamos " + d + ". Hecho:\n- " + strings.Join(done, "\n- ")
}

// ── Webhooks de llamadas (whatsapp.CallHandler) ────────────

// listen registra la llamada y le pasa lo que haya llegado antes.
func (c *Caller) listen(callID string, sig chan signal) {
	c.earlyMu.Lock()
	defer c.earlyMu.Unlock()
	c.live.Store(callID, sig)
	for _, s := range c.early[callID] {
		select {
		case sig <- s:
		default:
		}
	}
	delete(c.early, callID)
}

func (c *Caller) deliver(callID string, s signal) {
	c.earlyMu.Lock()
	defer c.earlyMu.Unlock()
	ch, ok := c.live.Load(callID)
	if !ok {
		if c.early == nil {
			c.early = map[string][]signal{}
		}
		if len(c.early) > 50 { // ids que nunca se reclamaron: no crecer sin límite
			clear(c.early)
		}
		c.early[callID] = append(c.early[callID], s)
		return
	}
	select {
	case ch.(chan signal) <- s:
	default:
	}
}

func (c *Caller) OnCallStatus(_ context.Context, callID, status string) {
	c.deliver(callID, signal{status: strings.ToUpper(status)})
}

func (c *Caller) OnCallEvent(ctx context.Context, ev whatsapp.CallEvent) {
	switch {
	case ev.Event == "terminate":
		c.deliver(ev.ID, signal{status: "TERMINATED", dur: ev.Duration})
	case ev.Event == "connect" && ev.Session != nil && ev.Session.SDPType == "answer":
		c.deliver(ev.ID, signal{status: "CONNECT", answer: ev.Session.SDP})
	case ev.Event == "connect" && ev.Session != nil && ev.Session.SDPType == "offer":
		go c.answer(context.WithoutCancel(ctx), ev)
	}
}

// answer atiende cuando el dueño llama al número de Mano. A los demás no se los atiende en Fase 0.
func (c *Caller) answer(ctx context.Context, ev whatsapp.CallEvent) {
	if ev.From != c.OwnerPhone {
		_ = c.Phone.RejectCall(ctx, ev.ID)
		return
	}
	if !c.busy.CompareAndSwap(false, true) {
		_ = c.Phone.RejectCall(ctx, ev.ID)
		return
	}
	defer c.busy.Store(false)
	if err := c.Models.AuthorizeVoice(ctx, c.estimate(), false); err != nil {
		_ = c.Phone.RejectCall(ctx, ev.ID)
		reason := err.Error()
		if b, ok := agent.IsBlocked(err); ok {
			reason = b.Reason
		}
		_ = c.Notify.Send(ctx, "No te atendí: "+reason)
		return
	}
	media, err := c.NewMedia()
	if err != nil {
		_ = c.Phone.RejectCall(ctx, ev.ID)
		return
	}
	defer media.Close()
	answer, err := media.AnswerPhone(ctx, ev.Session.SDP)
	if err == nil {
		err = c.Phone.AnswerCall(ctx, ev.ID, "pre_accept", answer)
	}
	if err == nil {
		err = c.Phone.AnswerCall(ctx, ev.ID, "accept", answer)
	}
	if err != nil {
		slog.Error("voz: no pude atender", "err", err)
		_ = c.Phone.RejectCall(ctx, ev.ID)
		return
	}
	sig := make(chan signal, 8)
	c.listen(ev.ID, sig)
	defer c.live.Delete(ev.ID)
	_ = c.Store.InsertCall(ctx, store.Call{ID: ev.ID, Direction: "in", Reason: "Me llamaste", Status: "accepted", Attempt: 1})
	c.talk(ctx, ev.ID, "in", c.ownerConvo(ctx, media, "El dueño te llamó. Preguntale en qué lo ayudás y contale lo pendiente si hay algo.", ""), media, sig)
}

// CallNow es /llamame: llama ya con lo pendiente, sin mirar horario ni tope.
func (c *Caller) CallNow(ctx context.Context) string {
	pending, _ := c.Store.PendingApprovals(ctx)
	reason := "Me pediste que te llame."
	if len(pending) > 0 {
		reason += fmt.Sprintf(" Tenés %d pendiente(s).", len(pending))
	}
	go func() {
		why, err := c.CallOwner(context.WithoutCancel(ctx), agent.CallRequest{Reason: reason, When: core.CallRequest{OwnerAsked: true}})
		switch {
		case err != nil:
			_ = c.Notify.Send(context.Background(), "No pude llamarte: "+err.Error())
		case why != "":
			_ = c.Notify.Send(context.Background(), "No te llamé: "+why+".")
		}
	}()
	return "Te llamo ahora."
}
