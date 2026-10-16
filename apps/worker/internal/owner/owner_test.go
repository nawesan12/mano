package owner

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nawesan12/mano/worker/internal/agent"
	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/testutil"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in, cmd, args string
		ok            bool
	}{
		{"/recordar@ManoBot Tema: dato", "recordar", "Tema: dato", true},
		{" /Resumen ", "resumen", "", true},
		{"hola", "", "", false},
	}
	for _, c := range cases {
		cmd, args, ok := ParseCommand(c.in)
		if cmd != c.cmd || args != c.args || ok != c.ok {
			t.Errorf("%q → %q %q %v", c.in, cmd, args, ok)
		}
	}
	if !DecisionRe.MatchString("ap:00000001-0000-4000-8000-000000000000:ok") || DecisionRe.MatchString("ap:x:ok") {
		t.Fatal("DecisionRe")
	}
}

func TestCallPolicyCommands(t *testing.T) {
	db := testutil.NewFakeRest(nil)
	srv := httptest.NewServer(db)
	defer srv.Close()
	s := &Service{Store: store.New(srv.URL, "service", testutil.Tenant)}
	var said []string
	say := func(t string) { said = append(said, t) }
	ctx := context.Background()

	s.callPolicy(ctx, "silencio", "22 a 7", say)
	s.callPolicy(ctx, "llamadas", "5", say)
	p, _ := s.Store.CallPolicy(ctx)
	if p.QuietFrom != "22:00" || p.QuietTo != "07:00" || p.MaxPerDay != 5 || !p.Enabled {
		t.Fatalf("política: %+v · %v", p, said)
	}
	s.callPolicy(ctx, "llamadas", "off", say)
	if p, _ = s.Store.CallPolicy(ctx); p.Enabled {
		t.Fatal("off apaga las llamadas")
	}
	s.callPolicy(ctx, "llamadas", "muchas", say)
	if !strings.Contains(said[len(said)-1], "Usalo así") {
		t.Fatal(said)
	}
}

func TestParseTransfer(t *testing.T) {
	d, amount, reason, problem := ParseTransfer("prov@mail.com 50.000 factura 88")
	if problem != "" || d.Email != "prov@mail.com" || amount != 50000 || reason != "factura 88" {
		t.Fatal(d, amount, reason, problem)
	}
	d, _, reason, problem = ParseTransfer("0170099-220000067890-12 1500 seña obra titular Juan Pérez dni 12.345.678")
	if problem != "" || d.CBU != "0170099220000067890"+"12" || d.Holder != "Juan Pérez" || d.DNI != "12345678" || reason != "seña obra" {
		t.Fatal(d, reason, problem)
	}
	if _, _, _, problem := ParseTransfer("prov@mail.com mucho"); problem == "" {
		t.Fatal("sin monto ni motivo")
	}
}

func TestShipmentsList(t *testing.T) {
	db := testutil.NewFakeRest(map[string][]map[string]any{"ml_shipments": {
		{"tenant_id": testutil.Tenant, "id": 5, "status": "ready_to_ship", "receiver": "Ana", "tracking": "TR1", "updated_at": "2026-10-15T10:00:00Z"},
		{"tenant_id": testutil.Tenant, "id": 6, "status": "delivered", "receiver": "Leo", "updated_at": "2026-10-15T11:00:00Z"},
	}})
	srv := httptest.NewServer(db)
	defer srv.Close()
	s := &Service{Store: store.New(srv.URL, "service", testutil.Tenant)}
	if got := s.shipments(context.Background()); got != "Envíos en curso: 1\n· 5 para despachar · Ana · seguimiento TR1" {
		t.Fatalf("%q", got)
	}
}

type sayCh struct{ sent []string }

func (c *sayCh) Send(_ context.Context, t string) error             { c.sent = append(c.sent, t); return nil }
func (c *sayCh) ApprovalCard(context.Context, store.Approval) error { return nil }

// "Acordate que…" se vuelve /recordar y la respuesta dice qué entendió, en un solo mensaje.
func TestNaturalLanguageRoutes(t *testing.T) {
	db := testutil.NewFakeRest(nil)
	srv := httptest.NewServer(db)
	defer srv.Close()
	st := store.New(srv.URL, "service", testutil.Tenant)
	cheap := &testutil.ScriptedLLM{T: core.Cheap, Reply: func(core.Request) string {
		return `{"accion":"recordar","tema":"Honorarios","dato":"3% más IVA"}`
	}}
	top := &testutil.ScriptedLLM{T: core.Top, Reply: func(core.Request) string { return "ok" }}
	models := agent.NewModels(cheap, top, st, core.Caps{DayUSD: 3, MonthUSD: 40})
	s := &Service{Agent: agent.New(st, models, nil, nil, nil, time.UTC), Models: models, Store: st}
	ch := &sayCh{}
	s.HandleText(context.Background(), ch, "Acordate que los honorarios son 3% más IVA")
	if len(ch.sent) != 1 || ch.sent[0] != "Entendí: /recordar Honorarios: 3% más IVA\nAnotado: Honorarios." {
		t.Fatalf("%q", ch.sent)
	}
	if len(db.Rows("memory_facts")) != 1 {
		t.Fatal("se guardó")
	}
}
