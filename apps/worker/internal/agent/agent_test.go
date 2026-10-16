package agent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/mercadopago"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/testutil"
)

type fakeNotifier struct{ sent, cards []string }

func (n *fakeNotifier) Send(_ context.Context, t string) error {
	n.sent = append(n.sent, t)
	return nil
}
func (n *fakeNotifier) ApprovalCard(_ context.Context, a store.Approval) error {
	n.cards = append(n.cards, a.ID)
	return nil
}

type fakeMail struct{ replies []store.Action }

func (m *fakeMail) Reply(_ context.Context, a store.Action) error {
	m.replies = append(m.replies, a)
	return nil
}

const tenant = testutil.Tenant

func setup(t *testing.T) (*Agent, *testutil.FakeRest, *testutil.ScriptedLLM, *testutil.ScriptedLLM, *fakeNotifier, *fakeMail) {
	t.Helper()
	db := testutil.NewFakeRest(map[string][]map[string]any{
		"rules": {
			{"id": "r-news", "tenant_id": tenant, "active": true, "priority": 10, "hits": 0,
				"match": map[string]any{"source": "gmail", "senderIncludesAny": []string{"newsletter"}}, "outcome": map[string]any{"urgency": "ignore"}},
		},
	})
	srv := httptest.NewServer(db)
	t.Cleanup(srv.Close)
	st := store.New(srv.URL, "service", tenant)

	cheap := &testutil.ScriptedLLM{T: core.Cheap, Reply: func(core.Request) string {
		return `{"urgency":"high","needsAction":true,"intent":"pedido de presupuesto","summary":"Ramírez pide tasación"}`
	}}
	top := &testutil.ScriptedLLM{T: core.Top, Reply: func(core.Request) string {
		return `{"title":"Responde a Ramírez con honorarios","risk":"medio","reply":"Hola Ramírez, te paso [PRECIO].","why":"Pidió presupuesto"}`
	}}
	models := NewModels(cheap, top, st, core.Caps{DayUSD: 3, MonthUSD: 40})
	n, mail := &fakeNotifier{}, &fakeMail{}
	return New(st, models, n, mail, nil, time.UTC), db, cheap, top, n, mail
}

func mail(id, from, body string) core.Event {
	return core.Event{TenantID: tenant, Source: "gmail", ExternalID: id, Kind: "email", Sender: from, Subject: "Tasación", Body: body,
		ReceivedAt: time.Now(), Payload: map[string]any{"threadId": "th1", "messageId": "<m1@mail>", "replyTo": from}}
}

func TestEndToEndApproveSendsReply(t *testing.T) {
	ctx := context.Background()
	a, db, cheap, top, n, m := setup(t)

	if err := a.OnEvent(ctx, mail("m1", "Ramírez <ramirez@mail.com>", "¿Me pasás presupuesto?")); err != nil {
		t.Fatal(err)
	}
	if cheap.Calls != 1 || top.Calls != 1 {
		t.Fatalf("cascada: cheap=%d top=%d", cheap.Calls, top.Calls)
	}
	if len(n.sent) != 1 || !strings.HasPrefix(n.sent[0], "Importante") || len(n.cards) != 1 {
		t.Fatalf("avisos: %v cards=%v", n.sent, n.cards)
	}

	// el mismo mail otra vez no se procesa
	_ = a.OnEvent(ctx, mail("m1", "Ramírez <ramirez@mail.com>", "¿Me pasás presupuesto?"))
	if cheap.Calls != 1 {
		t.Fatal("dedupe falló")
	}

	if got := a.OnDecision(ctx, n.cards[0], Approve, "test"); got != "Enviado a ramirez@mail.com." {
		t.Fatalf("decisión: %q", got)
	}
	if len(m.replies) != 1 || m.replies[0].ThreadID != "th1" || !strings.Contains(m.replies[0].Body, "[PRECIO]") {
		t.Fatalf("respuesta: %+v", m.replies)
	}
	if got := a.OnDecision(ctx, n.cards[0], Approve, "test"); !strings.Contains(got, "ya estaba resuelta") || len(m.replies) != 1 {
		t.Fatal("doble toque no debe reenviar")
	}
	if len(db.Rows("actions_log")) != 1 || len(db.Rows("usage")) != 2 {
		t.Fatalf("registro: log=%d usage=%d", len(db.Rows("actions_log")), len(db.Rows("usage")))
	}
}

func TestRuleSkipsModelsAndIgnoreSenderLearns(t *testing.T) {
	ctx := context.Background()
	a, db, cheap, _, n, _ := setup(t)

	_ = a.OnEvent(ctx, mail("m2", "newsletter@tienda.com", "Ofertas"))
	if cheap.Calls != 0 || len(n.sent) != 0 {
		t.Fatal("una regla debe resolver sin IA ni avisos")
	}

	_ = a.OnEvent(ctx, mail("m3", "Pesado <pesado@mail.com>", "¿Me pasás presupuesto?"))
	if got := a.OnDecision(ctx, n.cards[0], IgnoreSender, "test"); !strings.Contains(got, "pesado@mail.com") {
		t.Fatal(got)
	}
	if len(db.Rows("rules")) != 2 {
		t.Fatal("debió aprender una regla")
	}
	calls := cheap.Calls
	_ = a.OnEvent(ctx, mail("m4", "Pesado <pesado@mail.com>", "¿Y el presupuesto?"))
	if cheap.Calls != calls {
		t.Fatal("el remitente ignorado no debe gastar IA")
	}
}

func TestGovernorBlocksDraftAndWarns(t *testing.T) {
	ctx := context.Background()
	a, db, _, top, n, _ := setup(t)
	// ya se gastó el tope del día
	db.Set("spend_summary", []map[string]any{{"tenant_id": tenant, "day_usd": 3.2, "month_usd": 10.0, "hour_usd": 0.1}})

	_ = a.OnEvent(ctx, mail("m5", "Ramírez <ramirez@mail.com>", "¿Me pasás presupuesto?"))
	if top.Calls != 0 {
		t.Fatal("en el tope no debe usar el modelo top para algo no crítico")
	}
	var task map[string]any
	if len(db.Rows("tasks")) > 0 {
		task = db.Rows("tasks")[0]
	}
	if len(n.cards) != 0 || (task != nil && task["state"] != "escalated") {
		t.Fatalf("debía escalar sin tarjeta: cards=%v task=%v", n.cards, task)
	}
}

func TestOwnerSystem(t *testing.T) {
	s := OwnerSystem([]store.Fact{{Topic: "Tono", Fact: "de vos"}}, []store.Mission{{Area: "cobros", Goal: "No perder cobros", Autonomy: 2}})
	if !strings.Contains(s, "- Tono: de vos") || !strings.Contains(s, "[cobros, nivel 2]") {
		t.Fatal(s)
	}
}

type fakeCaller struct{ reqs []CallRequest }

func (c *fakeCaller) CallOwner(_ context.Context, r CallRequest) (string, error) {
	c.reqs = append(c.reqs, r)
	return "", nil
}

// Lo crítico llama (con la aprobación ya creada); lo importante no; una regla del dueño puede forzar o silenciar.
func TestCriticalCallsOwner(t *testing.T) {
	ctx := context.Background()
	a, db, _, _, n, _ := setup(t)
	fc := &fakeCaller{}
	a.SetCaller(fc)
	a.spawn = func(f func()) { f() }

	_ = a.OnEvent(ctx, mail("m6", "Ramírez <ramirez@mail.com>", "¿Me pasás presupuesto?"))
	if len(fc.reqs) != 0 {
		t.Fatal("lo importante no llama")
	}

	rules := db.Rows("rules")
	rules = append(rules,
		map[string]any{"id": "r-cbu", "tenant_id": tenant, "active": true, "priority": 20, "hits": 0,
			"match": map[string]any{"textIncludesAny": []string{"nuevo cbu"}}, "outcome": map[string]any{"urgency": "critical", "needsAction": true, "note": "Posible fraude"}},
		map[string]any{"id": "r-lili", "tenant_id": tenant, "active": true, "priority": 5, "hits": 0,
			"match": map[string]any{"senderIncludesAny": []string{"liliana"}}, "outcome": map[string]any{"urgency": "high", "notify": "call", "ignoreQuiet": true}},
		map[string]any{"id": "r-mudo", "tenant_id": tenant, "active": true, "priority": 6, "hits": 0,
			"match": map[string]any{"senderIncludesAny": []string{"contador"}}, "outcome": map[string]any{"urgency": "critical", "notify": "silent"}},
	)
	db.Set("rules", rules)

	_ = a.OnEvent(ctx, mail("m7", "Corralón <pagos@corralon.com>", "Les pasamos el nuevo CBU"))
	if len(fc.reqs) != 1 || fc.reqs[0].ApprovalID == "" || fc.reqs[0].When.Urgency != core.Critical || !strings.Contains(fc.reqs[0].Detail, "nuevo CBU") {
		t.Fatalf("lo crítico llama con la aprobación lista: %+v", fc.reqs)
	}
	_ = a.OnEvent(ctx, mail("m8", "Liliana <lili@mail.com>", "¿Hablamos?"))
	if len(fc.reqs) != 2 || !fc.reqs[1].When.Forced || !fc.reqs[1].When.IgnoreQuiet {
		t.Fatalf("la regla del dueño fuerza la llamada: %+v", fc.reqs)
	}
	sent := len(n.sent)
	_ = a.OnEvent(ctx, mail("m9", "Contador <contador@mail.com>", "Vence hoy"))
	if len(fc.reqs) != 2 || len(n.sent) != sent {
		t.Fatal("silent no llama ni avisa al momento")
	}
}

// "/regla llamame si escribe Liliana" → regla validada → aprobación → recién ahí se guarda.
func TestProposeRuleNeedsApproval(t *testing.T) {
	ctx := context.Background()
	a, db, cheap, _, _, _ := setup(t)
	cheap.Reply = func(core.Request) string {
		return `{"match":{"senderIncludesAny":["Liliana"]},"outcome":{"urgency":"high","notify":"call"},"summary":"Te llamo si escribe Liliana"}`
	}
	msg, ap := a.ProposeRule(ctx, "llamame si escribe Liliana")
	if ap == nil || ap.Action.Type != "rule.add" {
		t.Fatalf("tenía que proponer la regla: %q", msg)
	}
	if len(db.Rows("rules")) != 1 {
		t.Fatal("no se guarda antes de aprobar")
	}
	if got := a.OnDecision(ctx, ap.ID, Approve, "test"); !strings.Contains(got, "regla activa") {
		t.Fatal(got)
	}
	rules := db.Rows("rules")
	if len(rules) != 2 || rules[1]["origin"] != "owner" {
		t.Fatalf("regla guardada: %v", rules)
	}

	cheap.Reply = func(core.Request) string { return `{"match":{},"outcome":{"urgency":"high"}}` }
	if _, ap := a.ProposeRule(ctx, "algo"); ap != nil {
		t.Fatal("una regla vacía no se propone")
	}
}

// Un pendiente viejo se recuerda una sola vez; uno vencido se cierra sin ejecutar nada.
func TestFollowUpRemindsOnceAndExpires(t *testing.T) {
	ctx := context.Background()
	a, db, _, _, n, m := setup(t)
	db.Set("call_policy", []map[string]any{{"tenant_id": tenant, "enabled": true, "quiet_from": "21:00", "quiet_to": "08:00", "max_per_day": 3, "retry_after_min": 10, "max_attempts": 2}})
	now := time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)
	ts := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	db.Set("approvals", []map[string]any{
		{"id": "ap-viejo", "tenant_id": tenant, "status": "pending", "title": "Responder a Gómez", "area": "Inbox", "risk": "bajo", "action": map[string]any{"type": "gmail.reply"}, "created_at": ts(-5 * time.Hour), "expires_at": ts(67 * time.Hour)},
		{"id": "ap-nuevo", "tenant_id": tenant, "status": "pending", "title": "Responder a Paz", "area": "Inbox", "risk": "bajo", "action": map[string]any{"type": "gmail.reply"}, "created_at": ts(-time.Hour), "expires_at": ts(71 * time.Hour)},
		{"id": "ap-vencido", "tenant_id": tenant, "status": "pending", "title": "Mandar presupuesto", "area": "Inbox", "risk": "medio", "action": map[string]any{"type": "gmail.reply"}, "created_at": ts(-80 * time.Hour), "expires_at": ts(-8 * time.Hour)},
	})

	msg, err := a.FollowUp(ctx, now)
	if err != nil || !strings.Contains(msg, "Responder a Gómez (hace 5 h)") || strings.Contains(msg, "Paz") || !strings.Contains(msg, "Venció sin respuesta, no hice nada: Mandar presupuesto") {
		t.Fatalf("mensaje: %q %v", msg, err)
	}
	if again, _ := a.FollowUp(ctx, now.Add(time.Minute)); again != "" {
		t.Fatalf("no recuerda dos veces: %q", again)
	}
	status := map[string]any{}
	for _, r := range db.Rows("approvals") {
		status[r["id"].(string)] = r["status"]
	}
	if status["ap-vencido"] != "expired" || status["ap-viejo"] != "pending" {
		t.Fatalf("estados: %v", status)
	}
	if got := a.OnDecision(ctx, "ap-vencido", Approve, "test"); !strings.Contains(got, "ya estaba resuelta") || len(m.replies) != 0 {
		t.Fatal("lo vencido no se puede aprobar tarde")
	}
	if quiet, _ := a.FollowUp(ctx, time.Date(2026, 10, 15, 23, 0, 0, 0, time.UTC)); quiet != "" || len(n.sent) != 0 {
		t.Fatal("de noche no escribe")
	}

	closing, err := a.Closing(ctx)
	if err != nil || !strings.Contains(closing, "Venció sin respuesta: Mandar presupuesto") || !strings.Contains(closing, "Quedan 2") {
		t.Fatalf("cierre: %q %v", closing, err)
	}
}

type fakeSynth struct{ said []string }

func (f *fakeSynth) Model() string           { return "tts-test" }
func (f *fakeSynth) Estimate(string) float64 { return 0.001 }
func (f *fakeSynth) Synthesize(_ context.Context, text string) ([]byte, float64, error) {
	f.said = append(f.said, text)
	return []byte("OggS"), 0.001, nil
}

type voiceNotifier struct {
	fakeNotifier
	voices []string
}

func (v *voiceNotifier) SendVoice(_ context.Context, _ []byte, caption string) error {
	v.voices = append(v.voices, caption)
	return nil
}

// Lo importante llega como nota de voz (sin leer direcciones de mail) y queda en usage; sin TTS, texto.
func TestHighGoesAsVoiceNote(t *testing.T) {
	ctx := context.Background()
	a, db, _, _, _, _ := setup(t)
	vn := &voiceNotifier{}
	a.notify = vn
	synth := &fakeSynth{}
	a.models.TTS = synth

	_ = a.OnEvent(ctx, mail("v1", "Ramírez <ramirez@mail.com>", "¿Me pasás presupuesto?"))
	if len(vn.voices) != 1 || len(vn.sent) != 0 || !strings.HasPrefix(vn.voices[0], "Importante · Ramírez") {
		t.Fatalf("nota de voz en vez de texto: voces=%v textos=%v", vn.voices, vn.sent)
	}
	if !strings.Contains(synth.said[0], "Es de Ramírez.") || strings.Contains(synth.said[0], "@") {
		t.Fatalf("guion: %q", synth.said[0])
	}
	var voice bool
	for _, u := range db.Rows("usage") {
		voice = voice || u["tier"] == "voice"
	}
	if !voice {
		t.Fatal("la nota de voz pasa por el gobernador y queda en usage")
	}

	a.models.TTS = nil
	_ = a.OnEvent(ctx, mail("v2", "Paz <paz@mail.com>", "¿Me pasás presupuesto?"))
	if len(vn.voices) != 1 || len(vn.sent) != 1 {
		t.Fatal("sin TTS va por texto")
	}
}

func TestBriefIncludesAgenda(t *testing.T) {
	a, db, _, _, _, _ := setup(t)
	now := time.Now().In(time.UTC)
	at := func(h int) string {
		return time.Date(now.Year(), now.Month(), now.Day(), h, 30, 0, 0, time.UTC).Format(time.RFC3339)
	}
	db.Set("calendar_events", []map[string]any{
		{"tenant_id": tenant, "id": "1", "summary": "Firma contrato", "start_at": at(10), "end_at": at(11), "status": "confirmed"},
		{"tenant_id": tenant, "id": "2", "summary": "Cancelada", "start_at": at(12), "end_at": at(13), "status": "cancelled"},
		{"tenant_id": tenant, "id": "3", "summary": "Mañana", "start_at": now.Add(48 * time.Hour).Format(time.RFC3339), "end_at": now.Add(49 * time.Hour).Format(time.RFC3339), "status": "confirmed"},
	})
	b, err := a.Brief(context.Background())
	if err != nil || !strings.Contains(b, "Agenda de hoy: 10:30 Firma contrato.") {
		t.Fatalf("%q %v", b, err)
	}
}

type fakeCustomerCaller struct {
	fakeCaller
	calls []CustomerCall
}

func (f *fakeCustomerCaller) CallCustomer(_ context.Context, r CustomerCall) (string, error) {
	f.calls = append(f.calls, r)
	return "", nil
}

// /llamar arma la llamada como aprobación; recién al aprobar se llama al cliente.
func TestCustomerCallNeedsApproval(t *testing.T) {
	ctx := context.Background()
	a, db, _, _, _, _ := setup(t)
	fc := &fakeCustomerCaller{}
	a.SetCaller(fc)
	a.spawn = func(f func()) { f() }
	db.Set("wa_contacts", []map[string]any{
		{"tenant_id": tenant, "phone": "5491111111111", "name": "Juan Pérez", "is_owner": false},
		{"tenant_id": tenant, "phone": "5492222222222", "name": "Ana Pérez", "is_owner": false},
		{"tenant_id": tenant, "phone": "5493333333333", "name": "Gómez", "is_owner": false},
		{"tenant_id": tenant, "phone": "5492230000000", "name": "Yo", "is_owner": true},
	})
	if msg, ap := a.ProposeCustomerCall(ctx, "perez", "recordarle la factura"); ap != nil || !strings.Contains(msg, "Hay varios") {
		t.Fatalf("ambiguo: %q", msg)
	}
	if msg, ap := a.ProposeCustomerCall(ctx, "Fulano", "x"); ap != nil || !strings.Contains(msg, "No encontré") {
		t.Fatalf("no existe: %q", msg)
	}
	msg, ap := a.ProposeCustomerCall(ctx, "gomez", "Recordarle que la factura 123 vence hoy")
	if ap == nil || ap.Action.To != "5493333333333" || ap.Action.Type != "call.customer" {
		t.Fatalf("propuesta: %q %+v", msg, ap)
	}
	if len(fc.calls) != 0 {
		t.Fatal("no llama antes de aprobar")
	}
	if got := a.OnDecision(ctx, ap.ID, Approve, "test"); !strings.Contains(got, "Llamo a Gómez") {
		t.Fatal(got)
	}
	if len(fc.calls) != 1 || fc.calls[0].Goal != "Recordarle que la factura 123 vence hoy" || fc.calls[0].ApprovedBy != "test" {
		t.Fatalf("llamada: %+v", fc.calls)
	}
	if got := a.OnDecision(ctx, ap.ID, Approve, "test"); !strings.Contains(got, "ya estaba resuelta") || len(fc.calls) != 1 {
		t.Fatal("doble toque no vuelve a llamar")
	}
}

type fakePay struct {
	links   []string
	refunds []string
	payment mercadopago.Payment
}

func (f *fakePay) CreateLink(_ context.Context, title string, amount float64, ref, _ string, ttl time.Duration) (mercadopago.Link, error) {
	f.links = append(f.links, ref)
	return mercadopago.Link{ID: "pref-1", URL: "https://mpago.la/abc", Expires: time.Now().Add(ttl)}, nil
}
func (f *fakePay) Refund(_ context.Context, id string, amount float64, key string) (string, error) {
	f.refunds = append(f.refunds, id+":"+key)
	return "approved", nil
}
func (f *fakePay) Payment(context.Context, string) (mercadopago.Payment, error) {
	return f.payment, nil
}
func (f *fakePay) Recent(_ context.Context, days int) (mercadopago.Summary, error) {
	p := mercadopago.Payment{ID: 9, TransactionAmount: 150000}
	p.Payer.FirstName = "Juan"
	return mercadopago.Summary{Count: 3, Gross: 450000, Net: 430000, PendingRelease: 100000, Top: []mercadopago.Payment{p}}, nil
}

type fakeWA struct{ sent []string }

func (w *fakeWA) ReplyInWindow(_ context.Context, to, body string) error {
	w.sent = append(w.sent, to+": "+body)
	return nil
}

// /cobrar arma el link recién al aprobar y se lo manda al cliente; a los 3 días sin pagar, avisa una vez.
func TestChargeLinkFlow(t *testing.T) {
	ctx := context.Background()
	a, db, _, _, _, _ := setup(t)
	pay, wa := &fakePay{}, &fakeWA{}
	a.SetPayments(pay, "")
	a.wa = wa
	db.Set("wa_contacts", []map[string]any{{"tenant_id": tenant, "phone": "5491111111111", "name": "Juan Pérez", "is_owner": false}})

	_, ap := a.ProposeCharge(ctx, "perez", 150000, "factura 123")
	if ap == nil || ap.Action.Amount != 150000 || len(pay.links) != 0 || !strings.Contains(ap.Detail, "Hola Juan, te paso el link para pagar factura 123 ($150.000)") {
		t.Fatalf("propuesta: %+v · links %v", ap, pay.links)
	}
	if got := a.OnDecision(ctx, ap.ID, Approve, "test"); !strings.Contains(got, "Le mandé a Juan Pérez el link por $150.000") {
		t.Fatal(got)
	}
	if len(wa.sent) != 1 || !strings.Contains(wa.sent[0], "https://mpago.la/abc") || pay.links[0] != "mano-"+ap.ID {
		t.Fatalf("envío: %v %v", wa.sent, pay.links)
	}
	charges := db.Rows("mp_charges")
	if len(charges) != 1 || charges[0]["status"] != "pending" {
		t.Fatalf("cobro: %v", charges)
	}
	charges[0]["created_at"] = time.Now().Add(-80 * time.Hour).Format(time.RFC3339)
	db.Set("mp_charges", charges)
	db.Set("call_policy", []map[string]any{{"tenant_id": tenant, "enabled": true, "quiet_from": "00:00", "quiet_to": "00:00", "max_per_day": 3, "retry_after_min": 10, "max_attempts": 2}})
	msg, _ := a.FollowUp(ctx, time.Now())
	if !strings.Contains(msg, "Juan Pérez todavía no pagó el link de $150.000 (factura 123), hace 3 días") {
		t.Fatalf("seguimiento: %q", msg)
	}
	if again, _ := a.FollowUp(ctx, time.Now()); strings.Contains(again, "todavía no pagó") {
		t.Fatal("avisa una sola vez")
	}
	if m := a.Movements(ctx); !strings.Contains(m, "3 pago(s), $450.000 cobrado, $430.000 neto ($100.000 todavía por liberar)") || !strings.Contains(m, "Links sin pagar: 1 ($150.000)") {
		t.Fatalf("movimientos: %q", m)
	}
}

// Una devolución es riesgo alto y no se aprueba por voz; con el botón sí, con clave de idempotencia.
func TestRefundNeedsButton(t *testing.T) {
	ctx := context.Background()
	a, _, _, _, _, _ := setup(t)
	pay := &fakePay{payment: mercadopago.Payment{ID: 77, Status: "approved", TransactionAmount: 50000, Description: "Seña"}}
	pay.payment.Payer.Email = "juan@mail.com"
	a.SetPayments(pay, "")
	_, ap := a.ProposeRefund(ctx, "77", 0)
	if ap == nil || ap.Risk != "alto" || ap.Action.Amount != 50000 {
		t.Fatalf("propuesta: %+v", ap)
	}
	if got := a.OnDecision(ctx, ap.ID, Approve, "voz:549223"); !strings.Contains(got, "botón") || len(pay.refunds) != 0 {
		t.Fatalf("por voz no: %q", got)
	}
	if got := a.OnDecision(ctx, ap.ID, Approve, "telegram"); !strings.Contains(got, "devolví $50.000") || pay.refunds[0] != "77:mano-refund-"+ap.ID {
		t.Fatalf("con botón: %q %v", got, pay.refunds)
	}
	pay.payment.Status = "rejected"
	if msg, ap := a.ProposeRefund(ctx, "78", 0); ap != nil || !strings.Contains(msg, "no está acreditado") {
		t.Fatal(msg)
	}
}

type fakeTransfers struct {
	sent   []string
	status string
}

func (f *fakeTransfers) Send(_ context.Context, ref, _ string, d mercadopago.Destination, amount float64, _ string) (mercadopago.PayoutResult, error) {
	f.sent = append(f.sent, ref+":"+d.Email)
	return mercadopago.PayoutResult{ID: "POP1", Status: "created"}, nil
}
func (f *fakeTransfers) Status(context.Context, string) (mercadopago.PayoutResult, error) {
	return mercadopago.PayoutResult{ID: "POP1", Status: f.status}, nil
}

// Una transferencia: topes duros, riesgo alto, nunca por voz, y aviso cuando se acredita.
func TestPayoutLimitsAndButton(t *testing.T) {
	ctx := context.Background()
	a, db, _, _, _, _ := setup(t)
	if msg, ap := a.ProposePayout(ctx, mercadopago.Destination{Email: "p@mail.com"}, 1000, "x"); ap != nil || !strings.Contains(msg, "apagadas") {
		t.Fatal("apagadas por defecto:", msg)
	}
	tr := &fakeTransfers{status: "in_process"}
	a.SetTransfers(tr, TransferLimits{PerOp: 100000, PerDay: 150000}, "")
	dest := mercadopago.Destination{Email: "prov@mail.com"}
	if msg, ap := a.ProposePayout(ctx, dest, 200000, "factura"); ap != nil || !strings.Contains(msg, "tope por transferencia") {
		t.Fatal(msg)
	}
	_, ap := a.ProposePayout(ctx, dest, 90000, "factura 88")
	if ap == nil || ap.Risk != "alto" {
		t.Fatal("propuesta")
	}
	if got := a.OnDecision(ctx, ap.ID, Approve, "voz:5492230000000"); !strings.Contains(got, "botón") || len(tr.sent) != 0 {
		t.Fatal("por voz no:", got)
	}
	if got := a.OnDecision(ctx, ap.ID, Approve, "whatsapp"); !strings.Contains(got, "en camino") || tr.sent[0] != "mano-payout-"+ap.ID+":prov@mail.com" {
		t.Fatal(got, tr.sent)
	}
	if msg, ap := a.ProposePayout(ctx, dest, 90000, "otra"); ap != nil || !strings.Contains(msg, "tope diario") {
		t.Fatal("90.000 + 90.000 supera el tope diario de 150.000:", msg)
	}
	if lines := a.CheckPayouts(ctx); len(lines) != 0 {
		t.Fatal("sin resultado no avisa")
	}
	tr.status = "success"
	if lines := a.CheckPayouts(ctx); len(lines) != 1 || !strings.Contains(lines[0], "Se acreditó la transferencia de $90.000 a prov@mail.com") {
		t.Fatal(lines)
	}
	if db.Rows("mp_payouts")[0]["final"] != true {
		t.Fatal("queda cerrada")
	}
}

func TestIntentCommands(t *testing.T) {
	cases := []struct {
		in   intent
		want string
	}{
		{intent{Accion: "cobrar", Quien: "Pérez", Monto: 150000, Concepto: "factura 123"}, "/cobrar Pérez 150000 factura 123"},
		{intent{Accion: "llamar", Quien: "Gómez: x", Objetivo: "recordarle\nel pago"}, "/llamar Gómez x: recordarle el pago"},
		{intent{Accion: "transferir", Destino: "prov@mail.com", Monto: 5e4, Motivo: "factura 88"}, "/transferir prov@mail.com 50000 factura 88"},
		{intent{Accion: "devolver", Pago: "123", Monto: 0}, "/devolver 123"},
		{intent{Accion: "recordar", Tema: "Honorarios", Dato: "3% más IVA"}, "/recordar Honorarios: 3% más IVA"},
		{intent{Accion: "consulta", Que: "cobros"}, "/cobros"},
		{intent{Accion: "silencio", Desde: "22:00", Hasta: "07:00"}, "/silencio 22:00-07:00"},
	}
	for _, c := range cases {
		got, ok := c.in.command("x")
		if !ok || got != c.want {
			t.Errorf("%+v → %q", c.in, got)
		}
	}
	for _, bad := range []intent{{Accion: "cobrar", Quien: "Pérez"}, {Accion: "consulta", Que: "borrar todo"}, {Accion: "ninguna"}, {Accion: "/llamadas off"}} {
		if _, ok := bad.command("x"); ok {
			t.Errorf("no debería armar comando: %+v", bad)
		}
	}
	// barras en los campos no inyectan otro comando
	if got, _ := (intent{Accion: "recordar", Tema: "x", Dato: "/transferir a@b.com 1 y"}).command(""); got != "/recordar x: transferir a@b.com 1 y" {
		t.Fatal(got)
	}
}

func TestRouteOnlySpendsOnActions(t *testing.T) {
	ctx := context.Background()
	a, _, cheap, _, _, _ := setup(t)
	cheap.Reply = func(core.Request) string {
		return `{"accion":"cobrar","quien":"Pérez","monto":150000,"concepto":"factura 123"}`
	}
	if _, ok := a.Route(ctx, "¿Cómo viene el día?"); ok || cheap.Calls != 0 {
		t.Fatal("la charla no gasta el clasificador")
	}
	if c, ok := a.Route(ctx, "Cobrale 150 lucas a Pérez por la factura 123"); !ok || c != "/cobrar Pérez 150000 factura 123" || cheap.Calls != 1 {
		t.Fatalf("%q %v", c, ok)
	}
}

// Un proveedor siempre usó un CBU; si aparece otro, es crítico, llama y la cuenta nueva necesita el botón.
func TestFraudNewAccountIsCritical(t *testing.T) {
	ctx := context.Background()
	a, db, _, _, n, _ := setup(t)
	fc := &fakeCaller{}
	a.SetCaller(fc)
	a.spawn = func(f func()) { f() }
	const old, other = "2850590940090418135201", "0170099222000006789011"
	if !core.ValidCBU(other) {
		t.Fatal("CBU de prueba inválido")
	}
	_ = a.OnEvent(ctx, mail("f1", "Corralón <pagos@corralon.com>", "Te paso el CBU "+old+" para la factura"))
	if len(fc.reqs) != 0 {
		t.Fatal("la primera cuenta se confía")
	}
	_ = a.OnEvent(ctx, mail("f2", "Corralón <pagos@corralon.com>", "Pagá la factura al CBU "+old))
	if len(fc.reqs) != 0 {
		t.Fatal("la misma cuenta no alarma")
	}
	_ = a.OnEvent(ctx, mail("f3", "Corralón <pagos@corralon.com>", "Por favor transferí a este CBU "+other))
	if len(fc.reqs) != 1 || fc.reqs[0].When.Urgency != core.Critical {
		t.Fatalf("cuenta distinta = crítico y llamada: %+v", fc.reqs)
	}
	if !strings.Contains(n.sent[len(n.sent)-1], "CBU …9011; antes CBU …5201") {
		t.Fatalf("aviso: %v", n.sent)
	}
	var trust store.Approval
	for _, r := range db.Rows("approvals") {
		if r["area"] == "Antifraude" {
			trust.ID = r["id"].(string)
		}
	}
	if trust.ID == "" {
		t.Fatal("falta la tarjeta para confiar en la cuenta nueva")
	}
	if got := a.OnDecision(ctx, trust.ID, Approve, "voz:x"); !strings.Contains(got, "botón") {
		t.Fatal("confiar en una cuenta no se aprueba por voz")
	}
	before := len(db.Rows("approvals"))
	_ = a.OnEvent(ctx, mail("f4", "Corralón <pagos@corralon.com>", "Recordá: CBU "+other))
	if len(db.Rows("approvals")) > before+1 {
		t.Fatal("la tarjeta de confianza sale una sola vez")
	}
	_ = a.OnDecision(ctx, trust.ID, Approve, "telegram")
	calls := len(fc.reqs)
	_ = a.OnEvent(ctx, mail("f5", "Corralón <pagos@corralon.com>", "CBU "+other))
	if len(fc.reqs) != calls {
		t.Fatal("confirmada, ya no alarma")
	}
}
