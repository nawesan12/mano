package mercadopago

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/testutil"
)

func sign(secret, manifest string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(manifest))
	return hex.EncodeToString(m.Sum(nil))
}

func TestValidSignature(t *testing.T) {
	v1 := sign("s3cr3t", "id:abc123;request-id:req-1;ts:1704908010;")
	if !ValidSignature("s3cr3t", "ts=1704908010,v1="+v1, "req-1", "ABC123") {
		t.Fatal("firma válida (data.id va en minúsculas)")
	}
	for _, c := range [][4]string{
		{"otro", "ts=1704908010,v1=" + v1, "req-1", "abc123"},
		{"s3cr3t", "ts=1704908011,v1=" + v1, "req-1", "abc123"},
		{"s3cr3t", "v1=" + v1, "req-1", "abc123"},
		{"", "ts=1704908010,v1=" + v1, "req-1", "abc123"},
	} {
		if ValidSignature(c[0], c[1], c[2], c[3]) {
			t.Errorf("debería rechazar %v", c)
		}
	}
	// sin request-id esa parte del manifiesto se omite
	if !ValidSignature("s", "ts=1,v1="+sign("s", "id:9;ts:1;"), "", "9") {
		t.Fatal("manifiesto sin request-id")
	}
}

func TestToEvent(t *testing.T) {
	p := Payment{ID: 77, Status: "rejected", StatusDetail: "cc_rejected_insufficient_amount", TransactionAmount: 420000.5, CurrencyID: "ARS", Description: "Factura 123"}
	p.Payer.FirstName, p.Payer.LastName, p.Payer.Email = "Juan", "Pérez", "juan@mail.com"
	ev := ToEvent(p, "t1")
	if ev.ExternalID != "77:rejected" || ev.Kind != "payment.rejected" || *ev.Amount != 420000.5 || ev.Sender != "Juan Pérez <juan@mail.com>" {
		t.Fatalf("%+v", ev)
	}
	if !strings.Contains(ev.Body, "rechazado de ARS 420.000,50") || !strings.Contains(ev.Body, "insufficient") {
		t.Fatal(ev.Body)
	}
	if money(1500) != "1.500" || money(999) != "999" || money(1234567.25) != "1.234.567,25" {
		t.Fatal(money(1500), money(1234567.25))
	}
	if Relevant("pending") || !Relevant("charged_back") {
		t.Fatal("Relevant")
	}
}

type sink struct {
	mu  sync.Mutex
	evs []core.Event
}

func (s *sink) OnEvent(_ context.Context, ev core.Event) error {
	s.mu.Lock()
	s.evs = append(s.evs, ev)
	s.mu.Unlock()
	return nil
}

func TestWebhookFetchesPayment(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/payments/555" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"id":555,"status":"approved","transaction_amount":900000,"currency_id":"ARS","payer":{"email":"a@b.com"}}`))
	}))
	defer api.Close()
	c := NewClient("tok")
	c.base = api.URL
	s := &sink{}
	wh := &Webhook{Client: c, Secret: "sec", Sink: s, TenantID: "t1", Base: context.Background()}

	post := func(sig string) int {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/mercadopago?data.id=555&type=payment", strings.NewReader(`{"type":"payment","action":"payment.updated","data":{"id":"555"}}`))
		req.Header.Set("x-signature", sig)
		req.Header.Set("x-request-id", "r1")
		rec := httptest.NewRecorder()
		wh.ServeHTTP(rec, req)
		return rec.Code
	}
	if post("ts=1,v1=00") != http.StatusUnauthorized {
		t.Fatal("sin firma válida se rechaza")
	}
	if post("ts=1,v1="+sign("sec", "id:555;request-id:r1;ts:1;")) != http.StatusOK {
		t.Fatal("con firma válida")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.evs)
		s.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(s.evs) != 1 || s.evs[0].Kind != "payment.approved" || *s.evs[0].Amount != 900000 {
		t.Fatalf("evento: %+v", s.evs)
	}
}

func TestParseAmount(t *testing.T) {
	for in, want := range map[string]float64{"150000": 150000, "150.000": 150000, "$150.000,50": 150000.5, "150k": 150000} {
		if got, ok := ParseAmount(in); !ok || got != want {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	if _, ok := ParseAmount("mucho"); ok {
		t.Error("no es un monto")
	}
}

// Un pago de un link de Mano marca el cobro y lleva la marca que lo sube a importante.
func TestWebhookMarksChargePaid(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":556,"status":"approved","transaction_amount":150000,"currency_id":"ARS","external_reference":"mano-ap1"}`))
	}))
	defer api.Close()
	db := testutil.NewFakeRest(map[string][]map[string]any{"mp_charges": {{"id": "mano-ap1", "tenant_id": testutil.Tenant, "status": "pending", "name": "Juan", "description": "factura 123", "amount": 150000}}})
	rest := httptest.NewServer(db)
	defer rest.Close()
	c := NewClient("tok")
	c.base = api.URL
	s := &sink{}
	wh := &Webhook{Client: c, Charges: store.New(rest.URL, "service", testutil.Tenant), Sink: s, TenantID: testutil.Tenant, Base: context.Background()}
	wh.process("556")
	wh.process("556") // reintento: no vuelve a marcar
	if db.Rows("mp_charges")[0]["status"] != "paid" {
		t.Fatal("el cobro queda pagado")
	}
	if len(s.evs) != 2 || !strings.Contains(core.Norm(s.evs[0].Body), "link de cobro de mano a juan") || strings.Contains(s.evs[1].Body, "link de cobro") {
		t.Fatalf("eventos: %+v", s.evs)
	}
}
