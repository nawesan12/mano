// Package calendar vigila el Google Calendar del dueño: notificaciones push (nunca polling), sincronización
// incremental y detección, sin IA, de lo que importa: reuniones que otro movió o canceló, invitaciones y choques.
package calendar

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/google"
	"github.com/nawesan12/mano/worker/internal/store"
)

const apiBase = "https://www.googleapis.com/calendar/v3"

// ErrSyncGone: Google invalidó el sync token (410). Hay que sincronizar de cero.
var ErrSyncGone = errors.New("sync token vencido")

type Client struct {
	auth *google.Auth
	base string
	hc   *http.Client
}

func NewClient(auth *google.Auth) *Client {
	return &Client{auth: auth, base: apiBase, hc: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) api(ctx context.Context, method, path string, q url.Values, body, out any) error {
	tok, err := c.auth.Token(ctx)
	if err != nil {
		return err
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode == http.StatusGone {
		return ErrSyncGone
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("calendar %s %s: %d %s", method, path, res.StatusCode, core.Truncate(string(raw), 300))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// ── Modelo de Google ───────────────────────────────────────

type gTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
}

type gEvent struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Summary      string `json:"summary"`
	Transparency string `json:"transparency"`
	Start        gTime  `json:"start"`
	End          gTime  `json:"end"`
	Organizer    struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Self        bool   `json:"self"`
	} `json:"organizer"`
	Attendees []struct {
		Email string `json:"email"`
		Self  bool   `json:"self"`
	} `json:"attendees"`
}

func parseTime(t gTime, loc *time.Location) (time.Time, bool) {
	if t.DateTime != "" {
		v, err := time.Parse(time.RFC3339, t.DateTime)
		return v, err == nil
	}
	if t.Date != "" {
		v, err := time.ParseInLocation("2006-01-02", t.Date, loc)
		return v, err == nil
	}
	return time.Time{}, false
}

func toCal(e gEvent, loc *time.Location) store.CalEvent {
	start, _ := parseTime(e.Start, loc)
	end, _ := parseTime(e.End, loc)
	org := e.Organizer.DisplayName
	if org == "" {
		org = e.Organizer.Email
	}
	var att []string
	for _, a := range e.Attendees {
		if !a.Self && a.Email != "" {
			att = append(att, strings.ToLower(a.Email))
		}
	}
	return store.CalEvent{ID: e.ID, Summary: e.Summary, Start: start, End: end, AllDay: e.Start.Date != "" && e.Start.DateTime == "",
		Status: e.Status, Organizer: org, OrganizerMe: e.Organizer.Self, Busy: e.Transparency != "transparent", Attendees: att}
}

// Changes trae lo que cambió desde el token (o todo, si token == ""). Devuelve el próximo token.
func (c *Client) Changes(ctx context.Context, token string, loc *time.Location) ([]store.CalEvent, string, error) {
	var out []store.CalEvent
	page := ""
	for {
		q := url.Values{"singleEvents": {"true"}, "maxResults": {"250"}, "showDeleted": {"true"}}
		if token != "" {
			q.Set("syncToken", token)
		}
		if page != "" {
			q.Set("pageToken", page)
		}
		var res struct {
			Items         []gEvent `json:"items"`
			NextPageToken string   `json:"nextPageToken"`
			NextSyncToken string   `json:"nextSyncToken"`
		}
		if err := c.api(ctx, http.MethodGet, "/calendars/primary/events", q, nil, &res); err != nil {
			return nil, "", err
		}
		for _, e := range res.Items {
			out = append(out, toCal(e, loc))
		}
		if res.NextPageToken == "" {
			return out, res.NextSyncToken, nil
		}
		page = res.NextPageToken
	}
}

// Channel es una suscripción push activa.
type Channel struct {
	ID, ResourceID string
	Expires        time.Time
}

// Watch pide notificaciones push a address. Google corta a los ~7 días: renovar a diario.
func (c *Client) Watch(ctx context.Context, address, token string) (Channel, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := "mano-" + hex.EncodeToString(b)
	var res struct {
		ResourceID string `json:"resourceId"`
		Expiration string `json:"expiration"`
	}
	err := c.api(ctx, http.MethodPost, "/calendars/primary/events/watch", nil,
		map[string]any{"id": id, "type": "web_hook", "address": address, "token": token}, &res)
	if err != nil {
		return Channel{}, err
	}
	ch := Channel{ID: id, ResourceID: res.ResourceID}
	var ms int64
	if _, err := fmt.Sscan(res.Expiration, &ms); err == nil {
		ch.Expires = time.UnixMilli(ms)
	}
	return ch, nil
}

func (c *Client) Stop(ctx context.Context, ch Channel) error {
	return c.api(ctx, http.MethodPost, "/channels/stop", nil, map[string]string{"id": ch.ID, "resourceId": ch.ResourceID}, nil)
}

// ── Qué importa ────────────────────────────────────────────

// Lookahead: solo importa lo que pasa en las próximas dos semanas.
const Lookahead = 14 * 24 * time.Hour

func fmtWhen(t time.Time, loc *time.Location) string {
	days := []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}
	l := t.In(loc)
	return fmt.Sprintf("%s %d/%d %02d:%02d", days[l.Weekday()], l.Day(), int(l.Month()), l.Hour(), l.Minute())
}

func overlaps(a, b store.CalEvent) bool {
	return a.Start.Before(b.End) && b.Start.Before(a.End)
}

func blocking(e store.CalEvent) bool { return e.Status != "cancelled" && e.Busy && !e.AllDay }

// Diff compara la copia local con lo que cambió y arma los eventos para la cascada. Puro, sin IA.
// Lo que movió o canceló el propio dueño no se avisa: ya lo sabe.
func Diff(prev map[string]store.CalEvent, changed []store.CalEvent, now time.Time, loc *time.Location, tenantID string) []core.Event {
	var out []core.Event
	mk := func(kind string, e store.CalEvent, body string) core.Event {
		return core.Event{TenantID: tenantID, Source: "calendar", Kind: kind, ExternalID: e.ID + ":" + kind + ":" + e.Start.UTC().Format(time.RFC3339),
			Sender: e.Organizer, Subject: e.Summary, Body: body, ReceivedAt: now, Payload: map[string]any{"eventId": e.ID}}
	}
	// todos los eventos vigentes después del cambio, para buscar choques
	current := map[string]store.CalEvent{}
	for id, e := range prev {
		current[id] = e
	}
	for _, e := range changed {
		current[e.ID] = e
	}
	for _, e := range changed {
		old, existed := prev[e.ID]
		soon := e.Start.After(now) && e.Start.Before(now.Add(Lookahead))
		if !soon && !(existed && old.Start.After(now) && old.Start.Before(now.Add(Lookahead))) {
			continue
		}
		switch {
		case e.Status == "cancelled":
			if existed && old.Status != "cancelled" && !old.OrganizerMe {
				out = append(out, mk("calendar.cancelled", old, fmt.Sprintf("%s canceló \"%s\" del %s.", old.Organizer, old.Summary, fmtWhen(old.Start, loc))))
			}
			continue
		case existed && !e.OrganizerMe && !e.Start.Equal(old.Start):
			out = append(out, mk("calendar.moved", e, fmt.Sprintf("%s movió \"%s\" del %s al %s.", e.Organizer, e.Summary, fmtWhen(old.Start, loc), fmtWhen(e.Start, loc))))
		case !existed && !e.OrganizerMe:
			out = append(out, mk("calendar.invite", e, fmt.Sprintf("%s te invitó a \"%s\" el %s.", e.Organizer, e.Summary, fmtWhen(e.Start, loc))))
		}
		if !blocking(e) || !soon || (existed && e.Start.Equal(old.Start) && e.End.Equal(old.End)) {
			continue
		}
		for id, other := range current {
			if id != e.ID && blocking(other) && overlaps(e, other) {
				out = append(out, mk("calendar.conflict", e, fmt.Sprintf("\"%s\" (%s) se pisa con \"%s\" (%s).", e.Summary, fmtWhen(e.Start, loc), other.Summary, fmtWhen(other.Start, loc))))
				break
			}
		}
	}
	return out
}

// ── Servicio ───────────────────────────────────────────────

type EventSink interface {
	OnEvent(ctx context.Context, ev core.Event) error
}

// Service sincroniza y avisa. Es también el handler del webhook de Google.
type Service struct {
	Client    *Client
	Store     *store.Store
	Sink      EventSink
	Loc       *time.Location
	TenantID  string
	PushToken string
	Address   string // https://TU-WORKER/webhooks/calendar
	Base      context.Context

	mu      sync.Mutex // una sincronización a la vez: el sync token es secuencial
	chMu    sync.Mutex
	channel Channel
}

// Sync trae los cambios, los guarda y manda a la cascada lo que importa.
// La primera vez solo llena la copia local: no avisa de todo el calendario.
func (s *Service) Sync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.Store.CalendarSyncToken(ctx)
	if err != nil {
		return err
	}
	changed, next, err := s.Client.Changes(ctx, token, s.Loc)
	if errors.Is(err, ErrSyncGone) {
		_ = s.Store.ClearCalendar(ctx)
		token = ""
		changed, next, err = s.Client.Changes(ctx, "", s.Loc)
	}
	if err != nil {
		return err
	}
	prev := map[string]store.CalEvent{}
	if token != "" {
		cached, err := s.Store.CalendarEvents(ctx)
		if err != nil {
			return err
		}
		for _, e := range cached {
			prev[e.ID] = e
		}
		for _, ev := range Diff(prev, changed, time.Now(), s.Loc, s.TenantID) {
			if err := s.Sink.OnEvent(ctx, ev); err != nil {
				slog.Error("calendar evento", "err", err)
			}
		}
	}
	// solo se guarda lo que puede importar: de ayer a dos semanas
	keep := changed[:0]
	from, to := time.Now().Add(-24*time.Hour), time.Now().Add(Lookahead)
	for _, e := range changed {
		if e.Status == "cancelled" {
			// Google manda las cancelaciones sin horario: se marca la copia que ya teníamos
			if old, ok := prev[e.ID]; ok {
				old.Status = "cancelled"
				keep = append(keep, old)
			}
			continue
		}
		if e.Start.After(from) && e.Start.Before(to) {
			keep = append(keep, e)
		}
	}
	if err := s.Store.UpsertCalendarEvents(ctx, keep); err != nil {
		return err
	}
	return s.Store.SetCalendarSyncToken(ctx, next)
}

// Renew renueva el canal push (y para el anterior). Correr al arrancar y a diario.
func (s *Service) Renew(ctx context.Context) error {
	ch, err := s.Client.Watch(ctx, s.Address, s.PushToken)
	if err != nil {
		return err
	}
	s.chMu.Lock()
	old := s.channel
	s.channel = ch
	s.chMu.Unlock()
	if old.ID != "" {
		_ = s.Client.Stop(ctx, old)
	}
	return nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.PushToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Goog-Channel-Token")), []byte(s.PushToken)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Header.Get("X-Goog-Resource-State") == "sync" {
		return // aviso de que el canal arrancó
	}
	go func() {
		ctx, cancel := context.WithTimeout(s.Base, 2*time.Minute)
		defer cancel()
		if err := s.Sync(ctx); err != nil {
			slog.Error("calendar sync", "err", err)
		}
	}()
}

// CreateEvent crea un evento en el calendario del dueño e invita a los asistentes (requiere calendar.events).
func (c *Client) CreateEvent(ctx context.Context, title string, start, end time.Time, attendees []string, description string) (string, error) {
	body := map[string]any{
		"summary": title, "description": description,
		"start": map[string]string{"dateTime": start.Format(time.RFC3339)},
		"end":   map[string]string{"dateTime": end.Format(time.RFC3339)},
	}
	if len(attendees) > 0 {
		var as []map[string]string
		for _, a := range attendees {
			as = append(as, map[string]string{"email": a})
		}
		body["attendees"] = as
	}
	var out struct {
		ID       string `json:"id"`
		HTMLLink string `json:"htmlLink"`
	}
	q := url.Values{"sendUpdates": {"all"}}
	if err := c.api(ctx, http.MethodPost, "/calendars/primary/events", q, body, &out); err != nil {
		if strings.Contains(err.Error(), "403") {
			return "", errors.New("Google no me deja crear eventos: falta el permiso calendar.events en el refresh token")
		}
		return "", err
	}
	return out.HTMLLink, nil
}
