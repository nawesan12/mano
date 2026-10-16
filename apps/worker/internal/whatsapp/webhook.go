package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/owner"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/stt"
)

// EventSink recibe los mensajes de clientes como eventos (agent.Agent).
type EventSink interface {
	OnEvent(ctx context.Context, ev core.Event) error
}

// CallHandler recibe los eventos de llamadas (voice.Caller). Puede ser nil si la voz está apagada.
type CallHandler interface {
	OnCallEvent(ctx context.Context, ev CallEvent)
	OnCallStatus(ctx context.Context, callID, status string)
}

type Webhook struct {
	Client      *Client
	Store       *store.Store
	Owner       *owner.Service
	OwnerChan   *OwnerChannel
	Sink        EventSink
	Calls       CallHandler
	STT         *stt.Client
	AppSecret   string
	VerifyToken string
	PhoneID     string
	OwnerPhone  string
	TenantID    string
	Base        context.Context

	locks   sync.Map // un turno a la vez por contacto (ráfagas)
	limitMu sync.Mutex
	hits    map[string][]time.Time
}

// MaxPerMinute limita cuántos mensajes de un mismo cliente pasan al agente (anti-abuso de IA).
const MaxPerMinute = 20

func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.verify(rw, r)
	case http.MethodPost:
		w.receive(rw, r)
	default:
		rw.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// verify responde el desafío de Meta al registrar el webhook.
func (w *Webhook) verify(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("hub.mode") == "subscribe" && w.VerifyToken != "" && hmac.Equal([]byte(q.Get("hub.verify_token")), []byte(w.VerifyToken)) {
		_, _ = rw.Write([]byte(q.Get("hub.challenge")))
		return
	}
	rw.WriteHeader(http.StatusForbidden)
}

// ValidSignature comprueba X-Hub-Signature-256 en tiempo constante.
func ValidSignature(secret string, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok || secret == "" {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

func (w *Webhook) receive(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}
	if !ValidSignature(w.AppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		rw.WriteHeader(http.StatusOK) // nada que reintentar
		return
	}
	rw.WriteHeader(http.StatusOK) // responder rápido: Meta reintenta si tardamos
	go w.process(p)
}

// ── Payload de Meta (solo lo que usamos) ───────────────────

type Payload struct {
	Entry []struct {
		Changes []struct {
			Value Value `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type Value struct {
	Metadata struct {
		PhoneNumberID string `json:"phone_number_id"`
	} `json:"metadata"`
	Contacts []struct {
		WaID    string `json:"wa_id"`
		Profile struct {
			Name string `json:"name"`
		} `json:"profile"`
	} `json:"contacts"`
	Messages []Inbound   `json:"messages"`
	Statuses []Status    `json:"statuses"`
	Calls    []CallEvent `json:"calls"`
}

// CallEvent llega en el campo "calls": connect (con SDP) o terminate (con duración).
type CallEvent struct {
	ID        string       `json:"id"`
	From      string       `json:"from"`
	To        string       `json:"to"`
	Event     string       `json:"event"`     // connect | terminate
	Direction string       `json:"direction"` // BUSINESS_INITIATED | USER_INITIATED
	Timestamp string       `json:"timestamp"`
	Session   *CallSession `json:"session"`
	Status    string       `json:"status"` // terminate: COMPLETED | FAILED
	StartTime string       `json:"start_time"`
	EndTime   string       `json:"end_time"`
	Duration  int          `json:"duration"`
}

type Media struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Caption  string `json:"caption"`
}

type Inbound struct {
	From      string `json:"from"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Text      *struct {
		Body string `json:"body"`
	} `json:"text"`
	Interactive *struct {
		Type        string `json:"type"`
		ButtonReply *struct {
			ID string `json:"id"`
		} `json:"button_reply"`
		ListReply *struct {
			ID string `json:"id"`
		} `json:"list_reply"`
		CallPermissionReply *CallPermissionReply `json:"call_permission_reply"`
	} `json:"interactive"`
	Button *struct {
		Payload string `json:"payload"`
		Text    string `json:"text"`
	} `json:"button"`
	Audio    *Media `json:"audio"`
	Image    *Media `json:"image"`
	Document *Media `json:"document"`
	Video    *Media `json:"video"`
}

// CallPermissionReply es la respuesta del usuario al pedido de permiso de llamada.
type CallPermissionReply struct {
	Response            string `json:"response"` // accept | reject
	IsPermanent         bool   `json:"is_permanent"`
	ExpirationTimestamp string `json:"expiration_timestamp"`
}

type Status struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // "call" en los estados de llamadas
	Status  string `json:"status"`
	Pricing *struct {
		Billable *bool  `json:"billable"`
		Category string `json:"category"`
	} `json:"pricing"`
	Errors []struct {
		Title string `json:"title"`
	} `json:"errors"`
}

// ButtonID devuelve el id de un botón o lista tocado (contrato interno, nunca el texto visible).
func (m Inbound) ButtonID() string {
	if m.Interactive != nil {
		if m.Interactive.ButtonReply != nil {
			return m.Interactive.ButtonReply.ID
		}
		if m.Interactive.ListReply != nil {
			return m.Interactive.ListReply.ID
		}
	}
	if m.Button != nil {
		return m.Button.Payload
	}
	return ""
}

// Describe convierte el mensaje en texto para el triage sin gastar IA.
func (m Inbound) Describe() string {
	switch {
	case m.Text != nil:
		return m.Text.Body
	case m.Button != nil:
		return m.Button.Text
	case m.Image != nil:
		return strings.TrimSpace("[imagen] " + m.Image.Caption)
	case m.Document != nil:
		return strings.TrimSpace("[documento] " + m.Document.Caption)
	case m.Video != nil:
		return strings.TrimSpace("[video] " + m.Video.Caption)
	case m.Audio != nil:
		return "[audio]"
	}
	return "[" + m.Type + "]"
}

func (m Inbound) Time() time.Time {
	if s, err := strconv.ParseInt(m.Timestamp, 10, 64); err == nil {
		return time.Unix(s, 0)
	}
	return time.Now()
}

// ── Procesamiento ──────────────────────────────────────────

func (w *Webhook) process(p Payload) {
	ctx, cancel := context.WithTimeout(w.Base, 5*time.Minute)
	defer cancel()
	for _, e := range p.Entry {
		for _, ch := range e.Changes {
			v := ch.Value
			if v.Metadata.PhoneNumberID != w.PhoneID {
				continue // otro número: el tenant se resuelve por el canal, nunca por el usuario
			}
			for _, s := range v.Statuses {
				if s.Type == "call" || strings.HasPrefix(s.ID, "wacid.") {
					w.callStatus(ctx, s)
					continue
				}
				w.status(ctx, s)
			}
			for _, c := range v.Calls {
				w.call(ctx, c)
			}
			names := map[string]string{}
			for _, c := range v.Contacts {
				names[c.WaID] = c.Profile.Name
			}
			for _, m := range v.Messages {
				w.message(ctx, m, names[m.From])
			}
		}
	}
}

func (w *Webhook) status(ctx context.Context, s Status) {
	cat, errText := "", ""
	var billable *bool
	if s.Pricing != nil {
		cat, billable = s.Pricing.Category, s.Pricing.Billable
	}
	if len(s.Errors) > 0 {
		errText = s.Errors[0].Title
	}
	if err := w.Store.UpdateMessageStatus(ctx, s.ID, s.Status, cat, billable, errText); err != nil {
		slog.Warn("whatsapp status", "err", err)
	}
}

func (w *Webhook) message(ctx context.Context, m Inbound, name string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("whatsapp panic", "err", r)
		}
	}()
	fresh, err := w.Store.MarkProcessed(ctx, m.ID)
	if err != nil {
		slog.Error("whatsapp dedupe", "err", err)
		return
	}
	if !fresh {
		return // reintento de Meta
	}
	isOwner := m.From == w.OwnerPhone
	if err := w.Store.TouchContact(ctx, m.From, name, isOwner, m.Time()); err != nil {
		slog.Warn("whatsapp contacto", "err", err)
	}

	l, _ := w.locks.LoadOrStore(m.From, &sync.Mutex{})
	mu := l.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	if isOwner {
		w.fromOwner(ctx, m)
		return
	}
	w.fromCustomer(ctx, m, name)
}

// fresh dedupea por clave en processed_events (Meta reintenta los webhooks).
func (w *Webhook) fresh(ctx context.Context, key string) bool {
	ok, err := w.Store.MarkProcessed(ctx, key)
	if err != nil {
		slog.Error("whatsapp dedupe", "err", err)
	}
	return ok
}

func (w *Webhook) callStatus(ctx context.Context, s Status) {
	if w.Calls == nil || !w.fresh(ctx, "callst:"+s.ID+":"+s.Status) {
		return
	}
	w.Calls.OnCallStatus(ctx, s.ID, s.Status)
}

func (w *Webhook) call(ctx context.Context, c CallEvent) {
	if w.Calls == nil || !w.fresh(ctx, "call:"+c.ID+":"+c.Event) {
		return
	}
	w.Calls.OnCallEvent(ctx, c)
}

// permissionReply guarda el permiso de llamada que dio (o negó) el dueño.
func (w *Webhook) permissionReply(ctx context.Context, from string, r *CallPermissionReply) {
	kind := "none"
	var until *time.Time
	if r.Response == "accept" {
		kind = "temporary"
		if r.IsPermanent {
			kind = "permanent"
		} else if ts, err := strconv.ParseInt(r.ExpirationTimestamp, 10, 64); err == nil {
			t := time.Unix(ts, 0)
			until = &t
		}
	}
	if err := w.Store.SetCallPermission(ctx, from, kind, until); err != nil {
		slog.Error("whatsapp permiso de llamada", "err", err)
	}
	if from != w.OwnerPhone {
		return
	}
	msg := "Listo: cuando algo sea crítico, te llamo."
	if kind == "temporary" {
		msg = "Listo: te puedo llamar por 7 días. Si querés que sea siempre, dame el permiso permanente."
	} else if kind == "none" {
		msg = "Entendido, no te llamo. Te aviso por mensaje."
	}
	_ = w.OwnerChan.Send(ctx, msg)
}

func (w *Webhook) fromOwner(ctx context.Context, m Inbound) {
	ch, svc := w.OwnerChan, w.Owner
	if m.Interactive != nil && m.Interactive.CallPermissionReply != nil {
		w.permissionReply(ctx, m.From, m.Interactive.CallPermissionReply)
		return
	}
	switch {
	case m.ButtonID() != "":
		if !svc.HandleButton(ctx, ch, m.ButtonID(), "whatsapp:"+m.From) {
			_ = ch.Send(ctx, "Ese botón ya no está vigente. Escribí /pendientes.")
		}
	case m.Text != nil:
		svc.HandleText(ctx, ch, m.Text.Body)
	case m.Audio != nil:
		audio, mime, err := w.Client.Media(ctx, m.Audio.ID)
		if err != nil {
			_ = ch.Send(ctx, "No pude bajar el audio: "+err.Error())
			return
		}
		svc.HandleAudio(ctx, ch, audio, AudioFilename(mime))
	default:
		_ = ch.Send(ctx, "Por ahora entiendo texto, audios y botones.")
	}
}

func (w *Webhook) allow(phone string) bool {
	w.limitMu.Lock()
	defer w.limitMu.Unlock()
	if w.hits == nil {
		w.hits = map[string][]time.Time{}
	}
	now := time.Now()
	kept := w.hits[phone][:0]
	for _, t := range w.hits[phone] {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= MaxPerMinute {
		w.hits[phone] = kept
		return false
	}
	w.hits[phone] = append(kept, now)
	return true
}

// fromCustomer: Fase 0 no le contesta solo al cliente. Lo convierte en evento:
// triage, aviso al dueño si importa y borrador de respuesta para aprobar.
func (w *Webhook) fromCustomer(ctx context.Context, m Inbound, name string) {
	if !w.allow(m.From) {
		slog.Warn("whatsapp: ráfaga de cliente, se descarta", "phone_suffix", suffix(m.From))
		return
	}
	body := m.Describe()
	if m.Audio != nil && w.STT != nil {
		if audio, mime, err := w.Client.Media(ctx, m.Audio.ID); err == nil {
			if text, err := w.STT.Transcribe(ctx, audio, AudioFilename(mime)); err == nil && text != "" {
				body = "[audio] " + text
			}
		}
	}
	sender := "+" + m.From
	if name != "" {
		sender = name + " (+" + m.From + ")"
	}
	ev := core.Event{
		TenantID: w.TenantID, Source: "whatsapp", ExternalID: m.ID, Kind: "message",
		Sender: sender, Body: body, ReceivedAt: m.Time(),
		Payload: map[string]any{"phone": m.From, "name": name, "type": m.Type},
	}
	if err := w.Sink.OnEvent(ctx, ev); err != nil {
		slog.Error("whatsapp evento", "err", err)
	}
}

func suffix(phone string) string {
	if len(phone) <= 4 {
		return phone
	}
	return phone[len(phone)-4:]
}
