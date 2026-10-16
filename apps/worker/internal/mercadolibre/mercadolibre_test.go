package mercadolibre

import (
	"context"
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

type sink struct {
	mu  sync.Mutex
	evs []core.Event
}

func (s *sink) OnEvent(_ context.Context, e core.Event) error {
	s.mu.Lock()
	s.evs = append(s.evs, e)
	s.mu.Unlock()
	return nil
}

func (s *sink) n() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.evs)
}

// fakeML rota el refresh token en cada uso, como Mercado Libre: el viejo deja de servir.
func fakeML(t *testing.T) (*httptest.Server, *[]string) {
	var used []string
	var mu sync.Mutex
	valid := "r0"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/oauth/token":
			_ = r.ParseForm()
			got := r.Form.Get("refresh_token")
			used = append(used, got)
			if got != valid {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"invalid_grant"}`))
				return
			}
			valid = "r" + string(rune('0'+len(used)))
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"` + valid + `","expires_in":121,"user_id":99}`))
		case "/shipments/5":
			if r.Header.Get("x-format-new") != "true" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"id":5,"status":"not_delivered","substatus":"receiver_absent","tracking_number":"TR1","order_id":7,"destination":{"receiver_name":"Ana","shipping_address":{"city":{"name":"Mar del Plata"}}}}`))
		case "/orders/7":
			_, _ = w.Write([]byte(`{"id":7,"status":"paid","total_amount":35000,"buyer":{"nickname":"ANA77"},"order_items":[{"item":{"title":"Lámpara"},"quantity":2}],"shipping":{"id":5}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})), &used
}

func TestTokenRotationIsPersisted(t *testing.T) {
	api, used := fakeML(t)
	defer api.Close()
	db := testutil.NewFakeRest(nil)
	rest := httptest.NewServer(db)
	defer rest.Close()
	st := store.New(rest.URL, "service", testutil.Tenant)

	c := NewClient("app", "secret", "r0", st)
	c.base = api.URL
	if _, err := c.token(context.Background()); err != nil {
		t.Fatal(err)
	}
	// el access token vence (expires_in 121 - 120 s de margen): la próxima usa el refresh token NUEVO, guardado en la base
	time.Sleep(1100 * time.Millisecond)
	if _, err := c.token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*used, ",") != "r0,r1" {
		t.Fatalf("tokens usados: %v", *used)
	}
	// un worker nuevo (reinicio) arranca del token guardado, no del .env viejo
	c2 := NewClient("app", "secret", "r0", st)
	c2.base = api.URL
	if _, err := c2.token(context.Background()); err != nil {
		t.Fatal("después de un reinicio sigue andando:", err)
	}
}

func TestEvents(t *testing.T) {
	s := Shipment{ID: 5, Status: "ready_to_ship"}
	s.Destination.ReceiverName = "Ana"
	ev, ok := ShipmentEvent(s, "t")
	if !ok || ev.Kind != "shipment.ready_to_ship" || !strings.Contains(ev.Body, "Imprimí la etiqueta") {
		t.Fatal(ev)
	}
	if _, ok := ShipmentEvent(Shipment{Status: "handling"}, "t"); ok {
		t.Fatal("los intermedios no avisan")
	}
	o := Order{ID: 7, Status: "paid", TotalAmount: 35000}
	if ev, ok := OrderEvent(o, "t"); !ok || ev.Kind != "order.paid" || *ev.Amount != 35000 {
		t.Fatal(ev)
	}
}

func TestWebhookValidatesAndRefetches(t *testing.T) {
	api, _ := fakeML(t)
	defer api.Close()
	db := testutil.NewFakeRest(nil)
	rest := httptest.NewServer(db)
	defer rest.Close()
	st := store.New(rest.URL, "service", testutil.Tenant)
	c := NewClient("123", "secret", "r0", st)
	c.base = api.URL
	sk := &sink{}
	wh := &Webhook{Client: c, AppID: "123", UserID: 99, Sink: sk, Log: st, TenantID: testutil.Tenant, Base: context.Background()}

	post := func(body string) {
		rec := httptest.NewRecorder()
		wh.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhooks/mercadolibre", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Code)
		}
	}
	post(`{"resource":"/shipments/5","user_id":99,"topic":"shipments","application_id":999}`) // otra app
	post(`{"resource":"/shipments/5","user_id":1,"topic":"shipments","application_id":123}`)  // otro vendedor
	post(`{"resource":"/shipments/5","user_id":99,"topic":"shipments","application_id":123}`)
	post(`{"resource":"/orders/7","user_id":99,"topic":"orders_v2","application_id":123}`)
	deadline := time.Now().Add(3 * time.Second)
	for sk.n() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if sk.n() != 2 {
		t.Fatalf("solo los avisos válidos: %d", sk.n())
	}
	kinds := map[string]bool{}
	for _, e := range sk.evs {
		kinds[e.Kind] = true
	}
	if !kinds["shipment.not_delivered"] || !kinds["order.paid"] {
		t.Fatal(kinds)
	}
	if rows := db.Rows("ml_shipments"); len(rows) != 1 || rows[0]["tracking"] != "TR1" {
		t.Fatalf("envío guardado: %v", rows)
	}
}
