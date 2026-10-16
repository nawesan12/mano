package store

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Away es el modo ausencia: hasta cuándo, y la respuesta a clientes que el dueño aprobó.
type Away struct {
	Until     time.Time `json:"until_at"`
	StartedAt time.Time `json:"started_at"`
	Reply     string    `json:"reply"`
	Approved  bool      `json:"reply_approved"`
	Active    bool      `json:"active"`
}

func (s *Store) GetAway(ctx context.Context) (*Away, error) {
	var rows []Away
	q := s.q("select", "until_at,started_at,reply,reply_approved,active", "active", "is.true")
	if err := s.do(ctx, http.MethodGet, "away", q, nil, "", &rows); err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// StartAway arranca (o reemplaza) la ausencia y borra las respuestas de la anterior.
func (s *Store) StartAway(ctx context.Context, until time.Time, reply string) error {
	if err := s.do(ctx, http.MethodDelete, "away_replies", s.q(), nil, "", nil); err != nil {
		return err
	}
	row := map[string]any{"tenant_id": s.tenant, "until_at": until.UTC().Format(time.RFC3339), "started_at": time.Now().UTC().Format(time.RFC3339),
		"reply": reply, "reply_approved": false, "active": true}
	return s.do(ctx, http.MethodPost, "away", url.Values{"on_conflict": {"tenant_id"}}, []any{row}, "resolution=merge-duplicates", nil)
}

// ApproveAwayReply aprueba el texto SOLO si sigue siendo el de la ausencia activa (una tarjeta vieja no aprueba otro texto).
func (s *Store) ApproveAwayReply(ctx context.Context, reply string) (bool, error) {
	var out []struct {
		Active bool `json:"active"`
	}
	err := s.do(ctx, http.MethodPatch, "away", s.q("active", "is.true", "reply", "eq."+reply, "select", "active"), map[string]any{"reply_approved": true}, "return=representation", &out)
	return len(out) > 0, err
}

// EndAway termina la ausencia. Devuelve false si no había una activa (para no mandar dos resúmenes).
func (s *Store) EndAway(ctx context.Context) (bool, error) {
	var out []struct {
		Active bool `json:"active"`
	}
	err := s.do(ctx, http.MethodPatch, "away", s.q("active", "is.true", "select", "active"), map[string]any{"active": false}, "return=representation", &out)
	return len(out) > 0, err
}

// MarkAwayReplied anota que a ese cliente ya se le respondió. false = ya tenía respuesta (una sola por ausencia).
func (s *Store) MarkAwayReplied(ctx context.Context, phone string) (bool, error) {
	var out []struct {
		Phone string `json:"phone"`
	}
	q := url.Values{"on_conflict": {"tenant_id,phone"}, "select": {"phone"}}
	err := s.do(ctx, http.MethodPost, "away_replies", q, []any{map[string]any{"tenant_id": s.tenant, "phone": phone}}, "resolution=ignore-duplicates,return=representation", &out)
	return len(out) > 0, err
}

func (s *Store) AwayReplies(ctx context.Context) (int, error) {
	var out []struct {
		Phone string `json:"phone"`
	}
	err := s.do(ctx, http.MethodGet, "away_replies", s.q("select", "phone"), nil, "", &out)
	return len(out), err
}

// EventsSince cuenta los eventos por urgencia desde un momento (para el resumen de la ausencia).
func (s *Store) EventsSince(ctx context.Context, since time.Time) (map[string]int, error) {
	var rows []struct {
		Urgency    string    `json:"urgency"`
		ReceivedAt time.Time `json:"received_at"`
	}
	q := s.q("select", "urgency,received_at", "received_at", "gte."+since.UTC().Format(time.RFC3339), "limit", "5000")
	if err := s.do(ctx, http.MethodGet, "events", q, nil, "", &rows); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range rows {
		if !r.ReceivedAt.IsZero() && r.ReceivedAt.Before(since) {
			continue
		}
		u := r.Urgency
		if u == "" {
			u = "normal"
		}
		out[u]++
	}
	return out, nil
}
