package store

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// CalEvent es la copia local de un evento de Google Calendar: sirve para saber qué cambió.
type CalEvent struct {
	ID          string    `json:"id"`
	Summary     string    `json:"summary"`
	Start       time.Time `json:"start_at"`
	End         time.Time `json:"end_at"`
	AllDay      bool      `json:"all_day"`
	Status      string    `json:"status"` // confirmed | tentative | cancelled
	Organizer   string    `json:"organizer"`
	OrganizerMe bool      `json:"organizer_me"`
	Busy        bool      `json:"busy"` // false si está marcado como disponible (transparent)
	Attendees   []string  `json:"attendees"`
}

func (s *Store) CalendarSyncToken(ctx context.Context) (string, error) {
	var rows []struct {
		Token string `json:"sync_token"`
	}
	if err := s.do(ctx, http.MethodGet, "calendar_state", s.q("select", "sync_token"), nil, "", &rows); err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].Token, nil
}

func (s *Store) SetCalendarSyncToken(ctx context.Context, token string) error {
	row := map[string]any{"tenant_id": s.tenant, "sync_token": nullable(token), "updated_at": time.Now().UTC().Format(time.RFC3339)}
	return s.do(ctx, http.MethodPost, "calendar_state", url.Values{"on_conflict": {"tenant_id"}}, []any{row}, "resolution=merge-duplicates", nil)
}

// CalendarEvents devuelve la copia local, ordenada por inicio.
func (s *Store) CalendarEvents(ctx context.Context) ([]CalEvent, error) {
	var out []CalEvent
	q := s.q("select", "id,summary,start_at,end_at,all_day,status,organizer,organizer_me,busy,attendees", "order", "start_at")
	if err := s.do(ctx, http.MethodGet, "calendar_events", q, nil, "", &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

func (s *Store) UpsertCalendarEvents(ctx context.Context, evs []CalEvent) error {
	if len(evs) == 0 {
		return nil
	}
	rows := make([]any, len(evs))
	for i, e := range evs {
		rows[i] = map[string]any{
			"tenant_id": s.tenant, "id": e.ID, "summary": e.Summary, "start_at": e.Start.UTC().Format(time.RFC3339),
			"end_at": e.End.UTC().Format(time.RFC3339), "all_day": e.AllDay, "status": e.Status, "organizer": e.Organizer,
			"organizer_me": e.OrganizerMe, "busy": e.Busy, "attendees": orEmpty(e.Attendees), "updated_at": time.Now().UTC().Format(time.RFC3339),
		}
	}
	return s.do(ctx, http.MethodPost, "calendar_events", url.Values{"on_conflict": {"tenant_id,id"}}, rows, "resolution=merge-duplicates", nil)
}

// ClearCalendar borra la copia local (cuando Google invalida el sync token y hay que empezar de cero).
func (s *Store) ClearCalendar(ctx context.Context) error {
	return s.do(ctx, http.MethodDelete, "calendar_events", s.q(), nil, "", nil)
}
