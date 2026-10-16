package store

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
)

// ── Política de llamadas ───────────────────────────────────

// CallPolicy devuelve la política del tenant o los defaults si no hay fila.
func (s *Store) CallPolicy(ctx context.Context) (core.CallPolicy, error) {
	var rows []core.CallPolicy
	q := s.q("select", "enabled,quiet_from,quiet_to,max_per_day,retry_after_min,max_attempts")
	if err := s.do(ctx, http.MethodGet, "call_policy", q, nil, "", &rows); err != nil {
		return core.DefaultCallPolicy(), err
	}
	if len(rows) == 0 {
		return core.DefaultCallPolicy(), nil
	}
	return rows[0], nil
}

// SetCallPolicy guarda la política completa (upsert por tenant).
func (s *Store) SetCallPolicy(ctx context.Context, p core.CallPolicy) error {
	row := map[string]any{
		"tenant_id": s.tenant, "enabled": p.Enabled, "quiet_from": p.QuietFrom, "quiet_to": p.QuietTo,
		"max_per_day": p.MaxPerDay, "retry_after_min": p.RetryAfterMin, "max_attempts": p.MaxAttempts,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}
	return s.do(ctx, http.MethodPost, "call_policy", url.Values{"on_conflict": {"tenant_id"}}, []any{row}, "resolution=merge-duplicates", nil)
}

// ── Llamadas ───────────────────────────────────────────────

type Call struct {
	ID         string `json:"id"`
	Direction  string `json:"direction"` // out | in
	Party      string `json:"party"`     // owner | customer
	EventID    string `json:"event_id,omitempty"`
	ApprovalID string `json:"approval_id,omitempty"`
	Reason     string `json:"reason"`
	Status     string `json:"status"`
	Attempt    int    `json:"attempt"`
}

func (s *Store) InsertCall(ctx context.Context, c Call) error {
	return s.do(ctx, http.MethodPost, "calls", nil, map[string]any{
		"id": c.ID, "tenant_id": s.tenant, "direction": c.Direction, "party": partyOr(c.Party), "event_id": nullable(c.EventID),
		"approval_id": nullable(c.ApprovalID), "reason": core.Truncate(c.Reason, 300), "status": c.Status, "attempt": c.Attempt,
	}, "", nil)
}

// CallEnd es lo que se guarda al terminar una llamada.
type CallEnd struct {
	Status               string
	StartedAt, EndedAt   time.Time
	DurationS            int
	WACostUSD, AICostUSD float64
	Summary, Transcript  string
	Outcome              []string
}

func (s *Store) UpdateCallStatus(ctx context.Context, id, status string) error {
	return s.do(ctx, http.MethodPatch, "calls", s.q("id", "eq."+id), map[string]any{"status": status}, "", nil)
}

func (s *Store) FinishCall(ctx context.Context, id string, e CallEnd) error {
	patch := map[string]any{
		"status": e.Status, "duration_s": e.DurationS, "wa_cost_usd": e.WACostUSD, "ai_cost_usd": e.AICostUSD,
		"summary": core.Truncate(e.Summary, 1000), "transcript": core.Truncate(e.Transcript, 20000), "outcome": orEmpty(e.Outcome),
	}
	if !e.StartedAt.IsZero() {
		patch["started_at"] = e.StartedAt.UTC().Format(time.RFC3339)
	}
	if !e.EndedAt.IsZero() {
		patch["ended_at"] = e.EndedAt.UTC().Format(time.RFC3339)
	}
	return s.do(ctx, http.MethodPatch, "calls", s.q("id", "eq."+id), patch, "", nil)
}

func partyOr(p string) string {
	if p == "" {
		return "owner"
	}
	return p
}

func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// CallStats cuenta las llamadas al dueño de hoy y las sin atender seguidas más recientes.
func (s *Store) CallStats(ctx context.Context, loc *time.Location) (today, recentMissed int, err error) {
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	since := now.Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	var rows []struct {
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"created_at"`
	}
	q := s.q("select", "status,created_at", "direction", "eq.out", "party", "eq.owner", "created_at", "gte."+since, "order", "created_at.desc", "limit", "50")
	if err := s.do(ctx, http.MethodGet, "calls", q, nil, "", &rows); err != nil {
		return 0, 0, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	streak := true
	for _, r := range rows {
		if !r.CreatedAt.Before(start) {
			today++
		}
		if streak {
			if r.Status == "missed" {
				recentMissed++
			} else if r.Status != "dialing" && r.Status != "ringing" {
				streak = false
			}
		}
	}
	return today, recentMissed, nil
}

// ── Permiso de llamada ─────────────────────────────────────

func (s *Store) SetCallPermission(ctx context.Context, phone, kind string, until *time.Time) error {
	row := map[string]any{"tenant_id": s.tenant, "phone": phone, "call_permission": kind, "call_permission_until": nil}
	if until != nil {
		row["call_permission_until"] = until.UTC().Format(time.RFC3339)
	}
	return s.do(ctx, http.MethodPost, "wa_contacts", url.Values{"on_conflict": {"tenant_id,phone"}}, []any{row}, "resolution=merge-duplicates", nil)
}

// MarkPermissionRequested anota el pedido: WhatsApp deja 1 cada 24 h y 2 por semana.
func (s *Store) MarkPermissionRequested(ctx context.Context, phone string, at time.Time) error {
	row := map[string]any{"tenant_id": s.tenant, "phone": phone, "call_permission_requested_at": at.UTC().Format(time.RFC3339)}
	return s.do(ctx, http.MethodPost, "wa_contacts", url.Values{"on_conflict": {"tenant_id,phone"}}, []any{row}, "resolution=merge-duplicates", nil)
}

type CallPermission struct {
	Kind        string     `json:"call_permission"`
	Until       *time.Time `json:"call_permission_until"`
	RequestedAt *time.Time `json:"call_permission_requested_at"`
}

func (s *Store) CallPermissionOf(ctx context.Context, phone string) (CallPermission, error) {
	var rows []CallPermission
	q := s.q("phone", "eq."+phone, "select", "call_permission,call_permission_until,call_permission_requested_at")
	if err := s.do(ctx, http.MethodGet, "wa_contacts", q, nil, "", &rows); err != nil || len(rows) == 0 {
		return CallPermission{Kind: "none"}, err
	}
	return rows[0], nil
}

// Valid dice si el permiso sigue vigente en now.
func (p CallPermission) Valid(now time.Time) bool {
	switch p.Kind {
	case "permanent":
		return true
	case "temporary":
		return p.Until != nil && now.Before(*p.Until)
	}
	return false
}

// ── Contactos y negocio ────────────────────────────────────

type Contact struct {
	Phone   string `json:"phone"`
	Name    string `json:"name"`
	IsOwner bool   `json:"is_owner"`
}

// Contacts devuelve los contactos de WhatsApp (sin el dueño), para encontrar a quién llamar.
func (s *Store) Contacts(ctx context.Context) ([]Contact, error) {
	var out []Contact
	err := s.do(ctx, http.MethodGet, "wa_contacts", s.q("select", "phone,name,is_owner", "limit", "1000"), nil, "", &out)
	kept := out[:0]
	for _, c := range out {
		if !c.IsOwner {
			kept = append(kept, c)
		}
	}
	return kept, err
}

// TenantName es el nombre del negocio, para presentarse en una llamada.
func (s *Store) TenantName(ctx context.Context) string {
	var rows []struct {
		Name string `json:"name"`
	}
	if err := s.do(ctx, http.MethodGet, "tenants", url.Values{"id": {"eq." + s.tenant}, "select": {"name"}}, nil, "", &rows); err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].Name
}
