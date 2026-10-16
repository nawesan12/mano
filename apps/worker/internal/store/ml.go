package store

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"time"
)

func (s *Store) MLRefreshToken(ctx context.Context) (string, error) {
	var rows []struct {
		Token string `json:"refresh_token"`
	}
	if err := s.do(ctx, http.MethodGet, "ml_auth", s.q("select", "refresh_token"), nil, "", &rows); err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0].Token, nil
}

func (s *Store) SetMLRefreshToken(ctx context.Context, token string, userID int64) error {
	row := map[string]any{"tenant_id": s.tenant, "refresh_token": token, "user_id": userID, "updated_at": time.Now().UTC().Format(time.RFC3339)}
	return s.do(ctx, http.MethodPost, "ml_auth", url.Values{"on_conflict": {"tenant_id"}}, []any{row}, "resolution=merge-duplicates", nil)
}

type Shipment struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	Substatus string    `json:"substatus"`
	Receiver  string    `json:"receiver"`
	Tracking  string    `json:"tracking"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) UpsertShipment(ctx context.Context, id int64, status, substatus, receiver, tracking string) error {
	row := map[string]any{"tenant_id": s.tenant, "id": id, "status": status, "substatus": substatus, "receiver": receiver,
		"tracking": tracking, "updated_at": time.Now().UTC().Format(time.RFC3339)}
	return s.do(ctx, http.MethodPost, "ml_shipments", url.Values{"on_conflict": {"tenant_id,id"}}, []any{row}, "resolution=merge-duplicates", nil)
}

// OpenShipments son los envíos que todavía no terminaron (ni entregados ni cancelados), del más viejo al más nuevo.
func (s *Store) OpenShipments(ctx context.Context) ([]Shipment, error) {
	var out []Shipment
	q := s.q("select", "id,status,substatus,receiver,tracking,updated_at", "status", "not.in.(delivered,cancelled)", "order", "updated_at", "limit", "50")
	if err := s.do(ctx, http.MethodGet, "ml_shipments", q, nil, "", &out); err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, sh := range out {
		if sh.Status != "delivered" && sh.Status != "cancelled" {
			kept = append(kept, sh)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].UpdatedAt.Before(kept[j].UpdatedAt) })
	return kept, nil
}
