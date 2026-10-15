package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

// fakeRest imita lo justo de PostgREST: filtros eq/neq/is, insert, upsert con ignore, patch y delete.
type fakeRest struct {
	mu     sync.Mutex
	tables map[string][]map[string]any
	seq    int
}

func (f *fakeRest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	table := strings.TrimPrefix(r.URL.Path, "/rest/v1/")
	q := r.URL.Query()
	match := func(row map[string]any) bool {
		for k, vs := range q {
			switch k {
			case "select", "order", "limit", "on_conflict":
				continue
			}
			v := vs[0]
			cur := fmt.Sprint(row[k])
			if row[k] == nil {
				cur = ""
			}
			switch {
			case strings.HasPrefix(v, "eq.") && cur != v[3:]:
				return false
			case strings.HasPrefix(v, "neq.") && cur == v[4:]:
				return false
			case v == "is.null" && cur != "":
				return false
			case v == "is.true" && row[k] != true:
				return false
			case strings.HasPrefix(v, "ilike.") && !strings.EqualFold(cur, v[6:]):
				return false
			case strings.HasPrefix(v, "in.("):
				if !strings.Contains(v, cur) {
					return false
				}
			}
		}
		return true
	}
	var out []map[string]any
	body, _ := io.ReadAll(r.Body)
	switch r.Method {
	case http.MethodGet:
		for _, row := range f.tables[table] {
			if match(row) {
				out = append(out, row)
			}
		}
	case http.MethodPost:
		var rows []map[string]any
		if err := json.Unmarshal(body, &rows); err != nil {
			var one map[string]any
			_ = json.Unmarshal(body, &one)
			rows = []map[string]any{one}
		}
		for _, row := range rows {
			if table == "events" {
				dup := false
				for _, e := range f.tables["events"] {
					if e["external_id"] == row["external_id"] {
						dup = true
					}
				}
				if dup {
					continue
				}
			}
			f.seq++
			row["id"] = fmt.Sprintf("%08d-0000-4000-8000-000000000000", f.seq)
			if table == "rules" {
				row["active"] = true
			}
			if table == "approvals" {
				row["status"] = "pending"
			}
			f.tables[table] = append(f.tables[table], row)
			out = append(out, row)
		}
	case http.MethodPatch:
		var patch map[string]any
		_ = json.Unmarshal(body, &patch)
		for _, row := range f.tables[table] {
			if match(row) {
				for k, v := range patch {
					row[k] = v
				}
				out = append(out, row)
			}
		}
	case http.MethodDelete:
		keep := f.tables[table][:0]
		for _, row := range f.tables[table] {
			if match(row) {
				out = append(out, row)
			} else {
				keep = append(keep, row)
			}
		}
		f.tables[table] = keep
	}
	if out == nil {
		out = []map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(out)
}

type scriptedLLM struct {
	tier  core.Tier
	reply func(core.Request) string
	calls int
}

func (s *scriptedLLM) Tier() core.Tier { return s.tier }
func (s *scriptedLLM) Model() string   { return string(s.tier) + "-model" }
func (s *scriptedLLM) Complete(_ context.Context, r core.Request) (core.Result, error) {
	s.calls++
	return core.Result{Text: s.reply(r), Tier: s.tier, Model: s.Model(), InputTokens: 100, OutputTokens: 50, CostUSD: 0.01}, nil
}

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

const tenant = "00000000-0000-0000-0000-000000000001"

func setup(t *testing.T) (*Agent, *fakeRest, *scriptedLLM, *scriptedLLM, *fakeNotifier, *fakeMail) {
	t.Helper()
	db := &fakeRest{tables: map[string][]map[string]any{
		"rules": {
			{"id": "r-news", "tenant_id": tenant, "active": true, "priority": 10, "hits": 0,
				"match": map[string]any{"source": "gmail", "senderIncludesAny": []string{"newsletter"}}, "outcome": map[string]any{"urgency": "ignore"}},
		},
	}}
	srv := httptest.NewServer(db)
	t.Cleanup(srv.Close)
	st := store.New(srv.URL, "service", tenant)

	cheap := &scriptedLLM{tier: core.Cheap, reply: func(core.Request) string {
		return `{"urgency":"high","needsAction":true,"intent":"pedido de presupuesto","summary":"Ramírez pide tasación"}`
	}}
	top := &scriptedLLM{tier: core.Top, reply: func(core.Request) string {
		return `{"title":"Responde a Ramírez con honorarios","risk":"medio","reply":"Hola Ramírez, te paso [PRECIO].","why":"Pidió presupuesto"}`
	}}
	models := NewModels(cheap, top, st, core.Caps{DayUSD: 3, MonthUSD: 40})
	n, mail := &fakeNotifier{}, &fakeMail{}
	return New(st, models, n, mail, time.UTC), db, cheap, top, n, mail
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
	if cheap.calls != 1 || top.calls != 1 {
		t.Fatalf("cascada: cheap=%d top=%d", cheap.calls, top.calls)
	}
	if len(n.sent) != 1 || !strings.HasPrefix(n.sent[0], "Importante") || len(n.cards) != 1 {
		t.Fatalf("avisos: %v cards=%v", n.sent, n.cards)
	}

	// el mismo mail otra vez no se procesa
	_ = a.OnEvent(ctx, mail("m1", "Ramírez <ramirez@mail.com>", "¿Me pasás presupuesto?"))
	if cheap.calls != 1 {
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
	if len(db.tables["actions_log"]) != 1 || len(db.tables["usage"]) != 2 {
		t.Fatalf("registro: log=%d usage=%d", len(db.tables["actions_log"]), len(db.tables["usage"]))
	}
}

func TestRuleSkipsModelsAndIgnoreSenderLearns(t *testing.T) {
	ctx := context.Background()
	a, db, cheap, _, n, _ := setup(t)

	_ = a.OnEvent(ctx, mail("m2", "newsletter@tienda.com", "Ofertas"))
	if cheap.calls != 0 || len(n.sent) != 0 {
		t.Fatal("una regla debe resolver sin IA ni avisos")
	}

	_ = a.OnEvent(ctx, mail("m3", "Pesado <pesado@mail.com>", "¿Me pasás presupuesto?"))
	if got := a.OnDecision(ctx, n.cards[0], IgnoreSender, "test"); !strings.Contains(got, "pesado@mail.com") {
		t.Fatal(got)
	}
	if len(db.tables["rules"]) != 2 {
		t.Fatal("debió aprender una regla")
	}
	calls := cheap.calls
	_ = a.OnEvent(ctx, mail("m4", "Pesado <pesado@mail.com>", "¿Y el presupuesto?"))
	if cheap.calls != calls {
		t.Fatal("el remitente ignorado no debe gastar IA")
	}
}

func TestGovernorBlocksDraftAndWarns(t *testing.T) {
	ctx := context.Background()
	a, db, _, top, n, _ := setup(t)
	// ya se gastó el tope del día
	db.tables["spend_summary"] = []map[string]any{{"tenant_id": tenant, "day_usd": 3.2, "month_usd": 10.0, "hour_usd": 0.1}}

	_ = a.OnEvent(ctx, mail("m5", "Ramírez <ramirez@mail.com>", "¿Me pasás presupuesto?"))
	if top.calls != 0 {
		t.Fatal("en el tope no debe usar el modelo top para algo no crítico")
	}
	var task map[string]any
	if len(db.tables["tasks"]) > 0 {
		task = db.tables["tasks"][0]
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
