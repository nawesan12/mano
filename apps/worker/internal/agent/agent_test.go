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
