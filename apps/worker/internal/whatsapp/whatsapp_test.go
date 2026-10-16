package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nawesan12/mano/worker/internal/agent"
	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/owner"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/testutil"
)

const (
	phoneID   = "PHONE123"
	ownerTel  = "5492230000000"
	clientTel = "5491111111111"
	secret    = "app-secret"
)

// fakeGraph guarda cada mensaje que "manda" Mano.
type fakeGraph struct {
	mu   sync.Mutex
	sent []map[string]any
}

func (g *fakeGraph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var p map[string]any
	_ = json.NewDecoder(r.Body).Decode(&p)
	g.sent = append(g.sent, p)
	_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.out` + string(rune('A'+len(g.sent))) + `"}]}`))
}

func (g *fakeGraph) take() []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := g.sent
	g.sent = nil
	return out
}

type rig struct {
	wh    *Webhook
	db    *testutil.FakeRest
	graph *fakeGraph
	top   *testutil.ScriptedLLM
}

func setup(t *testing.T, template string) *rig {
	t.Helper()
	db := testutil.NewFakeRest(nil)
	rest := httptest.NewServer(db)
	graph := &fakeGraph{}
	gsrv := httptest.NewServer(graph)
	t.Cleanup(rest.Close)
	t.Cleanup(gsrv.Close)

	st := store.New(rest.URL, "service", testutil.Tenant)
	cheap := &testutil.ScriptedLLM{T: core.Cheap, Reply: func(core.Request) string {
		return `{"urgency":"high","needsAction":true,"intent":"consulta de turno","summary":"Pide turno para mañana"}`
	}}
	top := &testutil.ScriptedLLM{T: core.Top, Reply: func(r core.Request) string {
		if strings.Contains(r.System, "WhatsApp") {
			return `{"title":"Ofrece turno a las 10","risk":"bajo","reply":"Hola, mañana tengo a las 10. ¿Te sirve?","why":"Pidió turno"}`
		}
		return "Todo tranquilo."
	}}
	models := agent.NewModels(cheap, top, st, core.Caps{DayUSD: 3, MonthUSD: 40})

	c := NewClient(phoneID, "token", "v26.0", st)
	c.base = gsrv.URL
	ownerCh := NewOwnerChannel(c, ownerTel, template, "es_AR")
	ag := agent.New(st, models, owner.Fallback{ownerCh}, nil, c, time.UTC)
	svc := &owner.Service{Agent: ag, Models: models, Store: st}
	wh := &Webhook{Client: c, Store: st, Owner: svc, OwnerChan: ownerCh, Sink: ag, AppSecret: secret, VerifyToken: "verify",
		PhoneID: phoneID, OwnerPhone: ownerTel, TenantID: testutil.Tenant, Base: context.Background()}
	return &rig{wh: wh, db: db, graph: graph, top: top}
}

func inbound(from, id string, msg map[string]any) Payload {
	msg["from"], msg["id"], msg["timestamp"] = from, id, strconv.FormatInt(time.Now().Unix(), 10)
	raw, _ := json.Marshal(map[string]any{"entry": []any{map[string]any{"changes": []any{map[string]any{"value": map[string]any{
		"metadata": map[string]any{"phone_number_id": phoneID},
		"contacts": []any{map[string]any{"wa_id": from, "profile": map[string]any{"name": "Marta"}}},
		"messages": []any{msg},
	}}}}}})
	var p Payload
	_ = json.Unmarshal(raw, &p)
	return p
}

func text(body string) map[string]any {
	return map[string]any{"type": "text", "text": map[string]any{"body": body}}
}

func button(id string) map[string]any {
	return map[string]any{"type": "interactive", "interactive": map[string]any{"type": "button_reply", "button_reply": map[string]any{"id": id, "title": "Aprobar"}}}
}

func sign(body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestSignatureAndVerify(t *testing.T) {
	r := setup(t, "")
	body := []byte(`{"entry":[]}`)
	if !ValidSignature(secret, body, sign(body)) || ValidSignature(secret, body, "sha256=00") || ValidSignature(secret, body, "") {
		t.Fatal("firma")
	}

	rec := httptest.NewRecorder()
	r.wh.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sin firma debe rechazar: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body))
	rec = httptest.NewRecorder()
	r.wh.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("con firma: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	r.wh.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?hub.mode=subscribe&hub.verify_token=verify&hub.challenge=42", nil))
	if b, _ := io.ReadAll(rec.Body); string(b) != "42" {
		t.Fatalf("desafío: %q", b)
	}
	rec = httptest.NewRecorder()
	r.wh.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?hub.mode=subscribe&hub.verify_token=mal&hub.challenge=42", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatal("token de verificación incorrecto")
	}
}

// Cliente escribe → aviso al dueño por plantilla (ventana cerrada) → el dueño contesta →
// ve la tarjeta con botones → aprueba → Mano le responde al cliente dentro de su ventana.
func TestCustomerToApprovedReply(t *testing.T) {
	r := setup(t, "mano_aviso")

	r.wh.process(inbound(clientTel, "wamid.in1", text("Hola, ¿tienen turno mañana?")))
	sent := r.graph.take()
	if len(sent) != 2 || sent[0]["type"] != "template" || sent[1]["type"] != "template" {
		t.Fatalf("con la ventana del dueño cerrada solo se usan plantillas: %v", sent)
	}
	// reintento de Meta: no se procesa dos veces
	r.wh.process(inbound(clientTel, "wamid.in1", text("Hola, ¿tienen turno mañana?")))
	if len(r.graph.take()) != 0 || r.top.Calls != 1 {
		t.Fatal("dedupe falló")
	}

	r.wh.process(inbound(ownerTel, "wamid.in2", text("/pendientes")))
	sent = r.graph.take()
	if len(sent) != 1 || sent[0]["type"] != "interactive" || sent[0]["to"] != ownerTel {
		t.Fatalf("la tarjeta con botones va dentro de la ventana: %v", sent)
	}
	btns := sent[0]["interactive"].(map[string]any)["action"].(map[string]any)["buttons"].([]any)
	okID := btns[0].(map[string]any)["reply"].(map[string]any)["id"].(string)

	r.wh.process(inbound(ownerTel, "wamid.in3", button(okID)))
	sent = r.graph.take()
	if len(sent) != 2 || sent[0]["to"] != clientTel || sent[1]["to"] != ownerTel {
		t.Fatalf("respuesta al cliente y confirmación al dueño: %v", sent)
	}
	if body := sent[0]["text"].(map[string]any)["body"]; body != "Hola, mañana tengo a las 10. ¿Te sirve?" {
		t.Fatalf("texto enviado: %v", body)
	}
	if len(r.db.Rows("wa_messages")) != 5 {
		t.Fatalf("todo mensaje saliente queda registrado: %d", len(r.db.Rows("wa_messages")))
	}
}

func TestClosedWindowAndNoTemplate(t *testing.T) {
	r := setup(t, "") // sin plantilla configurada
	err := r.wh.OwnerChan.Send(context.Background(), "hola")
	if err != ErrWindowClosed || len(r.graph.take()) != 0 {
		t.Fatalf("sin ventana ni plantilla no debe mandar nada: %v", err)
	}
	if err := r.wh.Client.ReplyInWindow(context.Background(), clientTel, "hola"); err != ErrWindowClosed {
		t.Fatal("a un cliente que nunca escribió no se le responde libre")
	}
}

func TestStatusStoresPricing(t *testing.T) {
	r := setup(t, "")
	r.db.Set("wa_messages", []map[string]any{{"wamid": "wamid.X", "tenant_id": testutil.Tenant, "status": "sent"}})
	raw := `{"entry":[{"changes":[{"value":{"metadata":{"phone_number_id":"PHONE123"},"statuses":[{"id":"wamid.X","status":"delivered","pricing":{"billable":true,"category":"service"}}]}}]}]}`
	var p Payload
	_ = json.Unmarshal([]byte(raw), &p)
	r.wh.process(p)
	row := r.db.Rows("wa_messages")[0]
	if row["status"] != "delivered" || row["category"] != "service" || row["billable"] != true {
		t.Fatalf("estado: %v", row)
	}
}

func TestHelpers(t *testing.T) {
	if got := TemplateParam("línea 1\nlínea 2\t\tx     y"); strings.ContainsAny(got, "\n\t") || strings.Contains(got, "    ") {
		t.Fatalf("parámetro de plantilla: %q", got)
	}
	if AudioFilename("audio/ogg; codecs=opus") != "nota.ogg" || AudioFilename("audio/mp4") != "nota.m4a" {
		t.Fatal("AudioFilename")
	}
	m := Inbound{Type: "image", Image: &Media{Caption: "comprobante"}}
	if m.Describe() != "[imagen] comprobante" {
		t.Fatal(m.Describe())
	}
	w := &Webhook{}
	for i := 0; i < MaxPerMinute; i++ {
		if !w.allow("x") {
			t.Fatal("límite antes de tiempo")
		}
	}
	if w.allow("x") {
		t.Fatal("ráfaga sin límite")
	}
}
