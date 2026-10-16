package agent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
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
