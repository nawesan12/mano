package core

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustRule(t *testing.T, id string, prio int, match, outcome string) Rule {
	t.Helper()
	r, err := ParseRule(id, prio, []byte(match), []byte(outcome))
	if err != nil {
		t.Fatalf("regla %s: %v", id, err)
	}
	return r
}

func testRules(t *testing.T) []Rule {
	return []Rule{
		mustRule(t, "big", 30, `{"source":"mercadopago","amountGte":500000}`, `{"urgency":"high"}`),
		mustRule(t, "cbu", 20, `{"textIncludesAny":["nuevo CBU","cambio de cbu"]}`, `{"urgency":"critical","needsAction":true,"note":"Posible fraude"}`),
		mustRule(t, "news", 10, `{"source":"gmail","senderIncludesAny":["newsletter","no-reply"]}`, `{"urgency":"ignore"}`),
	}
}

func f(v float64) *float64 { return &v }

func TestParseRuleRejectsBadRows(t *testing.T) {
	for _, c := range []struct{ m, o string }{
		{`{"nope":true}`, `{"urgency":"ignore"}`},
		{`{}`, `{"urgency":"ignore"}`},
		{`{"source":"gmail"}`, `{"urgency":"urgentísimo"}`},
	} {
		if _, err := ParseRule("x", 1, []byte(c.m), []byte(c.o)); err == nil {
			t.Errorf("debería rechazar %s / %s", c.m, c.o)
		}
	}
}

func TestRulesMatching(t *testing.T) {
	rs := testRules(t)
	cases := []struct {
		name string
		ev   Event
		want string
	}{
		{"tildes y mayúsculas", Event{Source: "gmail", Body: "Les informamos el NUEVO cbu"}, "cbu"},
		{"tilde en la regla del texto", Event{Source: "gmail", Body: "hubo un cambio de CBÚ"}, "cbu"},
		{"prioridad", Event{Source: "gmail", Sender: "newsletter@banco.com", Body: "nuevo cbu"}, "news"},
		{"monto alto", Event{Source: "mercadopago", Amount: f(600000)}, "big"},
		{"monto bajo", Event{Source: "mercadopago", Amount: f(100)}, ""},
	}
	for _, c := range cases {
		got := ""
		if r := FirstMatch(rs, c.ev); r != nil {
			got = r.ID
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

type fakeLLM struct {
	text  string
	calls int
}

func (f *fakeLLM) Tier() Tier    { return Cheap }
func (f *fakeLLM) Model() string { return "fake" }
func (f *fakeLLM) Complete(context.Context, Request) (Result, error) {
	f.calls++
	return Result{Text: f.text, Tier: Cheap, CostUSD: 0.00005}, nil
}

func TestTriageCascade(t *testing.T) {
	ctx := context.Background()
	rs := testRules(t)

	cheap := &fakeLLM{text: "{}"}
	tr := RunTriage(ctx, Event{Source: "gmail", Body: "nuevo CBU"}, rs, cheap)
	if tr.Via != "rule" || tr.Urgency != Critical || tr.CostUSD != 0 || cheap.calls != 0 {
		t.Fatalf("una regla debe resolver sin modelo: %+v calls=%d", tr, cheap.calls)
	}

	cheap = &fakeLLM{text: "```json\n{\"urgency\":\"high\",\"needsAction\":true,\"intent\":\"presupuesto\",\"summary\":\"Ramírez pide tasación\"}\n```"}
	tr = RunTriage(ctx, Event{Source: "gmail", Sender: "ramirez@mail.com", Body: "¿Me pasás presupuesto?"}, rs, cheap)
	if tr.Via != "classifier" || tr.Urgency != High || !tr.NeedsAction || !NeedsTop(tr) {
		t.Fatalf("clasificador: %+v", tr)
	}

	tr = RunTriage(ctx, Event{Source: "gmail", Body: "hola"}, rs, &fakeLLM{text: "no sé"})
	if tr.Urgency != Normal || tr.NeedsAction {
		t.Fatalf("basura del modelo debe quedar para el resumen: %+v", tr)
	}

	tr = RunTriage(ctx, Event{Source: "gmail", Body: "hola"}, rs, nil)
	if tr.Urgency != Normal {
		t.Fatalf("sin modelo barato: %+v", tr)
	}
}

func TestGovernor(t *testing.T) {
	c := Caps{DayUSD: 3, MonthUSD: 40}
	modes := map[Spend]Mode{
		{1, 5, 0.2}:   ModeNormal,
		{2.5, 5, 0.2}: ModeDegraded,
		{1, 33, 0.2}:  ModeDegraded,
		{3, 5, 0.2}:   ModeCriticalOnly,
		{1.6, 5, 1.6}: ModeHalted,
	}
	for s, want := range modes {
		if got := GovernorMode(s, c); got != want {
			t.Errorf("%+v: got %s want %s", s, got, want)
		}
	}
	if d := Decide(Top, false, -1, Spend{2.5, 5, 0.1}, c); d.Tier != Cheap {
		t.Error("cerca del tope debe bajar al barato")
	}
	if d := Decide(Top, true, -1, Spend{2.5, 5, 0.1}, c); d.Tier != Top {
		t.Error("lo crítico mantiene el top antes del tope")
	}
	if d := Decide(Top, false, -1, Spend{3.1, 5, 0.1}, c); d.OK {
		t.Error("en el tope no pasa lo no crítico")
	}
	if d := Decide(Top, true, -1, Spend{3.1, 5, 0.1}, c); !d.OK || d.Tier != Cheap {
		t.Error("en el tope lo crítico pasa con el barato")
	}
	if d := Decide(Top, false, 2, Spend{1.5, 5, 0.1}, c); d.OK || !strings.Contains(d.Reason, "USD 2.00") {
		t.Errorf("debe avisar el costo: %+v", d)
	}
}

func TestProvidersOverHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/v1/messages":
			if r.Header.Get("x-api-key") != "k" {
				w.WriteHeader(401)
				return
			}
			if _, ok := body["system"].([]any); !ok {
				t.Error("anthropic: system debe ir como bloque cacheable")
			}
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hola"}],"usage":{"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":10000}}`))
		case "/chat/completions":
			if body["response_format"] == nil {
				t.Error("openai-compatible: falta response_format con JSON")
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"a\":1}"}}],"usage":{"prompt_tokens":1000000,"completion_tokens":500000}}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	a := NewLLM(ModelConfig{Tier: Top, Provider: "anthropic", Model: "m", APIKey: "k", BaseURL: srv.URL, PriceIn: 3, PriceOut: 15})
	r, err := a.Complete(ctx, Request{System: "s", Messages: []Message{{Role: "user", Content: "x"}}})
	if err != nil || r.Text != "hola" {
		t.Fatalf("anthropic: %v %+v", err, r)
	}
	// 1000 + 10000*0.1 = 2000 tokens de entrada facturados; 500 de salida
	if want := (2000*3 + 500*15) / 1e6; math.Abs(r.CostUSD-want) > 1e-9 {
		t.Errorf("costo anthropic %.6f want %.6f", r.CostUSD, want)
	}

	o := NewLLM(ModelConfig{Tier: Cheap, Provider: "openai-compatible", Model: "deepseek-flash", BaseURL: srv.URL, PriceIn: 0.3, PriceOut: 1.2})
	r, err = o.Complete(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}, JSON: true})
	if err != nil || math.Abs(r.CostUSD-0.9) > 1e-9 {
		t.Fatalf("openai-compatible: %v %+v", err, r)
	}
}

func TestParseJSON(t *testing.T) {
	var v struct{ A int }
	if err := ParseJSON(`dale: {"a":1} listo`, &v); err != nil || v.A != 1 {
		t.Fatal(err)
	}
	if Norm("Cambió el CBU") != "cambio el cbu" {
		t.Fatal(Norm("Cambió el CBU"))
	}
}

func TestRuleNotify(t *testing.T) {
	r := mustRule(t, "lili", 5, `{"senderIncludesAny":["liliana"]}`, `{"urgency":"high","notify":"call","ignoreQuiet":true}`)
	tr := RunTriage(context.Background(), Event{Source: "whatsapp", Sender: "Liliana (+54911)"}, []Rule{r}, nil)
	if tr.Notify != NotifyCall || !tr.IgnoreQuiet {
		t.Fatalf("la regla tiene que pasar notify e ignoreQuiet: %+v", tr)
	}
	for _, o := range []string{`{"urgency":"high","notify":"gritar"}`, `{"urgency":"ignore","notify":"call"}`} {
		if _, err := ParseRule("x", 1, []byte(`{"source":"gmail"}`), []byte(o)); err == nil {
			t.Errorf("debería rechazar %s", o)
		}
	}
}

func TestShouldCall(t *testing.T) {
	p := DefaultCallPolicy()
	day := time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)
	night := time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	early := time.Date(2026, 10, 16, 7, 59, 0, 0, time.UTC)
	cases := []struct {
		name          string
		r             CallRequest
		now           time.Time
		today, missed int
		want          bool
	}{
		{"crítico de día", CallRequest{Urgency: Critical}, day, 0, 0, true},
		{"alto no llama", CallRequest{Urgency: High}, day, 0, 0, false},
		{"regla fuerza", CallRequest{Urgency: High, Forced: true}, day, 0, 0, true},
		{"silencio de noche", CallRequest{Urgency: Critical}, night, 0, 0, false},
		{"silencio cruza medianoche", CallRequest{Urgency: Critical}, early, 0, 0, false},
		{"regla ignora silencio", CallRequest{Urgency: Critical, IgnoreQuiet: true}, night, 0, 0, true},
		{"tope diario", CallRequest{Urgency: Critical}, day, 3, 0, false},
		{"dos sin atender", CallRequest{Urgency: Critical}, day, 0, 2, false},
		{"el dueño lo pide", CallRequest{OwnerAsked: true}, night, 9, 9, true},
	}
	for _, c := range cases {
		if got := ShouldCall(p, c.r, c.now, c.today, c.missed); got.Call != c.want {
			t.Errorf("%s: got %v (%s) want %v", c.name, got.Call, got.Reason, c.want)
		}
	}
	off := p
	off.Enabled = false
	if ShouldCall(off, CallRequest{Urgency: Critical}, day, 0, 0).Call {
		t.Error("apagado no llama")
	}
}

func TestParseQuiet(t *testing.T) {
	for in, want := range map[string]string{"21-8": "21:00-08:00", "22:30 a 7": "22:30-07:00", "23hs-6hs": "23:00-06:00"} {
		a, b, err := ParseQuiet(in)
		if err != nil || a+"-"+b != want {
			t.Errorf("%q: got %s-%s %v want %s", in, a, b, err, want)
		}
	}
	if _, _, err := ParseQuiet("25-8"); err == nil {
		t.Error("25 no es una hora")
	}
}

func TestExtractAccounts(t *testing.T) {
	// CBU de ejemplo con verificadores correctos
	good := "2850590940090418135201"
	if !ValidCBU(good) {
		t.Fatal("el CBU de ejemplo tiene que ser válido")
	}
	text := "Nuevos datos: CBU " + good[:4] + " " + good[4:8] + "-" + good[8:] + ". Alias: CORRALON.NORTE.PAGOS. Factura 00012345678901234567890 (no es CBU)."
	got := ExtractAccounts(text)
	if len(got) != 2 || got[0] != "cbu:"+good || got[1] != "alias:corralon.norte.pagos" {
		t.Fatalf("%v", got)
	}
	if MaskAccount(got[0]) != "CBU …5201" {
		t.Fatal(MaskAccount(got[0]))
	}
}
