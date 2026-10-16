package store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// Since trae filas de una tabla desde un momento. El filtro de fecha se repite en Go (sirve igual con el PostgREST falso).
func (s *Store) Since(ctx context.Context, table, cols, timeCol string, since time.Time) ([]map[string]any, error) {
	var rows []map[string]any
	q := s.q("select", cols+","+timeCol, timeCol, "gte."+since.UTC().Format(time.RFC3339), "limit", "10000")
	if err := s.do(ctx, http.MethodGet, table, q, nil, "", &rows); err != nil {
		return nil, err
	}
	kept := rows[:0]
	for _, r := range rows {
		if v, ok := r[timeCol].(string); ok {
			if t, err := time.Parse(time.RFC3339, v); err == nil && t.Before(since) {
				continue
			}
		}
		kept = append(kept, r)
	}
	return kept, nil
}

// SaveReport guarda el diagnóstico de la semana para el panel.
func (s *Store) SaveReport(ctx context.Context, weekStart time.Time, data any, text string) error {
	raw, _ := json.Marshal(data)
	row := map[string]any{"tenant_id": s.tenant, "week_start": weekStart.Format("2006-01-02"), "data": json.RawMessage(raw), "text": text,
		"created_at": time.Now().UTC().Format(time.RFC3339)}
	return s.do(ctx, http.MethodPost, "reports", url.Values{"on_conflict": {"tenant_id,week_start"}}, []any{row}, "resolution=merge-duplicates", nil)
}
