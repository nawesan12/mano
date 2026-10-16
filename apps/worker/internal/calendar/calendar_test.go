package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/google"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/testutil"
)

var ar, _ = time.LoadLocation("America/Argentina/Buenos_Aires")

func ev(id, summary string, start time.Time, mins int, me bool) store.CalEvent {
	return store.CalEvent{ID: id, Summary: summary, Start: start, End: start.Add(time.Duration(mins) * time.Minute), Status: "confirmed",
		Organizer: "Liliana", OrganizerMe: me, Busy: true}
}

func kinds(evs []core.Event) string {
	var k []string
	for _, e := range evs {
		k = append(k, e.Kind)
	}
	return strings.Join(k, ",")
}

func TestDiff(t *testing.T) {
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, ar)
	at := func(day, h int) time.Time { return time.Date(2026, 10, day, h, 0, 0, 0, ar) }
	prev := map[string]store.CalEvent{
		"a": ev("a", "Firma contrato", at(16, 10), 60, false),
		"b": ev("b", "Dentista", at(16, 15), 60, true),
		"c": ev("c", "Visita obra", at(17, 9), 60, false),
		"d": ev("d", "Mía", at(17, 18), 60, true),
	}
	moved := ev("a", "Firma contrato", at(16, 15), 60, false) // Liliana la pasó a las 15: se pisa con el dentista
	cancelled := store.CalEvent{ID: "c", Status: "cancelled"}
	mine := ev("d", "Mía", at(17, 19), 60, true) // la movió el dueño: no se avisa
	invite := ev("e", "Café con Gómez", at(20, 11), 30, false)
	far := ev("f", "Congreso", at(30, 11), 60, false) // fuera de las dos semanas

	got := Diff(prev, []store.CalEvent{moved, cancelled, mine, invite, far}, now, ar, "t1")
	if k := kinds(got); k != "calendar.moved,calendar.conflict,calendar.cancelled,calendar.invite" {
		t.Fatalf("eventos: %s", k)
	}
	if !strings.Contains(got[0].Body, "Liliana movió \"Firma contrato\" del viernes 16/10 10:00 al viernes 16/10 15:00") {
		t.Fatal(got[0].Body)
	}
	if !strings.Contains(got[1].Body, "se pisa con \"Dentista\"") || !strings.Contains(got[2].Body, "canceló \"Visita obra\"") {
		t.Fatal(got[1].Body, got[2].Body)
	}
	if got[0].ExternalID == got[1].ExternalID {
		t.Fatal("cada aviso tiene su id")
	}
}

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

// fakeGoogle sirve el token y la lista de eventos según el syncToken.
func fakeGoogle(t *testing.T, pages map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"access_token":"at","expires_in":3600}`))
		case r.URL.Path == "/calendars/primary/events":
			body, ok := pages[r.URL.Query().Get("syncToken")]
			if !ok {
				w.WriteHeader(http.StatusGone)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func item(id, status, start string, me bool) map[string]any {
	end, _ := time.Parse(time.RFC3339, start)
	return map[string]any{"id": id, "status": status, "summary": "Reunión " + id, "start": map[string]string{"dateTime": start},
		"end": map[string]string{"dateTime": end.Add(time.Hour).Format(time.RFC3339)}, "organizer": map[string]any{"email": "lili@mail.com", "self": me}}
}

func page(next string, items ...map[string]any) string {
	b, _ := json.Marshal(map[string]any{"items": items, "nextSyncToken": next})
	return string(b)
}

// Primera sincronización: llena la copia sin avisar. Después: solo avisa lo que cambió. Un 410 rehace todo.
func TestSyncFlow(t *testing.T) {
	tomorrow := time.Now().Add(24 * time.Hour).Truncate(time.Hour).UTC()
	ts := func(d time.Duration) string { return tomorrow.Add(d).Format(time.RFC3339) }
	g := fakeGoogle(t, map[string]string{
		"":     page("tok1", item("x", "confirmed", ts(0), false)),
		"tok1": page("tok2", item("x", "confirmed", ts(3*time.Hour), false)),
		"tok2": page("tok3", map[string]any{"id": "x", "status": "cancelled"}),
	})
	defer g.Close()
	db := testutil.NewFakeRest(nil)
	rest := httptest.NewServer(db)
	defer rest.Close()

	auth := google.NewAuth("id", "secret", "refresh")
	auth.TokenURL = g.URL + "/token"
	c := NewClient(auth)
	c.base = g.URL
	sk := &sink{}
	svc := &Service{Client: c, Store: store.New(rest.URL, "service", testutil.Tenant), Sink: sk, Loc: ar, TenantID: testutil.Tenant, PushToken: "pt", Base: context.Background()}
	ctx := context.Background()

	if err := svc.Sync(ctx); err != nil || len(sk.evs) != 0 {
		t.Fatalf("la primera vez no avisa: %v %v", err, sk.evs)
	}
	if err := svc.Sync(ctx); err != nil || kinds(sk.evs) != "calendar.moved" {
		t.Fatalf("movida: %v %s", err, kinds(sk.evs))
	}
	if err := svc.Sync(ctx); err != nil || kinds(sk.evs) != "calendar.moved,calendar.cancelled" {
		t.Fatalf("cancelada: %v %s", err, kinds(sk.evs))
	}
	if rows := db.Rows("calendar_events"); len(rows) != 1 || rows[0]["status"] != "cancelled" {
		t.Fatalf("copia local: %v", rows)
	}
	// tok3 no existe en el falso: 410 → resincroniza de cero sin avisar
	if err := svc.Sync(ctx); err != nil || len(sk.evs) != 2 {
		t.Fatalf("410: %v %d", err, len(sk.evs))
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/calendar", nil)
	req.Header.Set("X-Goog-Channel-Token", "mal")
	svc.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("token incorrecto")
	}
}
