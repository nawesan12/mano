package store

import (
	"context"
	"net/http"
	"time"
)

// Charge es un link de pago que Mano mandó a un cliente.
type Charge struct {
	ID          string     `json:"id"` // = external_reference (mano-<aprobación>)
	PrefID      string     `json:"pref_id"`
	Phone       string     `json:"phone"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Amount      float64    `json:"amount"`
	URL         string     `json:"url"`
	Status      string     `json:"status"` // pending | paid | expired
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	RemindedAt  *time.Time `json:"reminded_at"`
}

func (s *Store) InsertCharge(ctx context.Context, c Charge) error {
	row := map[string]any{"id": c.ID, "tenant_id": s.tenant, "pref_id": c.PrefID, "phone": nullable(c.Phone), "name": c.Name,
		"description": c.Description, "amount": c.Amount, "url": c.URL, "status": "pending"}
	if c.ExpiresAt != nil {
		row["expires_at"] = c.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return s.do(ctx, http.MethodPost, "mp_charges", nil, row, "", nil)
}

// MarkChargePaid marca el link como pagado. Devuelve el cobro si estaba pendiente (para avisar una sola vez).
func (s *Store) MarkChargePaid(ctx context.Context, id string, paymentID int64) (*Charge, error) {
	var out []Charge
	q := s.q("id", "eq."+id, "status", "eq.pending", "select", "id,pref_id,phone,name,description,amount,url,status")
	err := s.do(ctx, http.MethodPatch, "mp_charges", q, map[string]any{"status": "paid", "payment_id": paymentID, "paid_at": time.Now().UTC().Format(time.RFC3339)}, "return=representation", &out)
	if err != nil || len(out) == 0 {
		return nil, err
	}
	return &out[0], nil
}

func (s *Store) PendingCharges(ctx context.Context) ([]Charge, error) {
	var out []Charge
	q := s.q("status", "eq.pending", "select", "id,pref_id,phone,name,description,amount,url,status,created_at,expires_at,reminded_at", "order", "created_at")
	return out, s.do(ctx, http.MethodGet, "mp_charges", q, nil, "", &out)
}

func (s *Store) SetChargeStatus(ctx context.Context, id, status string) error {
	return s.do(ctx, http.MethodPatch, "mp_charges", s.q("id", "eq."+id), map[string]any{"status": status}, "", nil)
}

func (s *Store) MarkChargeReminded(ctx context.Context, id string, at time.Time) error {
	return s.do(ctx, http.MethodPatch, "mp_charges", s.q("id", "eq."+id), map[string]any{"reminded_at": at.UTC().Format(time.RFC3339)}, "", nil)
}
