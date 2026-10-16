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
