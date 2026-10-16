package store

import (
	"context"
	"net/http"
	"net/url"

	"github.com/nawesan12/mano/worker/internal/core"
)

// Business devuelve el nombre y el rubro del negocio.
func (s *Store) Business(ctx context.Context) (name, vertical string, err error) {
	var rows []struct {
		Name     string `json:"name"`
		Vertical string `json:"vertical"`
	}
	if err = s.do(ctx, http.MethodGet, "tenants", url.Values{"id": {"eq." + s.tenant}, "select": {"name,vertical"}}, nil, "", &rows); err != nil || len(rows) == 0 {
		return "", "", err
	}
	return rows[0].Name, rows[0].Vertical, nil
}

// ApplyPlaybook carga las reglas y misiones del rubro. Si había otro rubro, saca las suyas; las del dueño no se tocan.
func (s *Store) ApplyPlaybook(ctx context.Context, p core.Playbook, name string) error {
	_, old, err := s.Business(ctx)
	if err != nil {
		return err
	}
	if old != "" {
		for _, table := range []string{"rules", "missions"} {
			if err := s.do(ctx, http.MethodDelete, table, s.q("playbook", "eq."+old), nil, "", nil); err != nil {
				return err
			}
		}
	}
	rules := make([]any, len(p.Rules))
	for i, r := range p.Rules {
		rules[i] = map[string]any{"tenant_id": s.tenant, "priority": 60 + i, "match": r.Match, "outcome": r.Outcome, "origin": "playbook", "playbook": p.Key}
	}
	if err := s.do(ctx, http.MethodPost, "rules", nil, rules, "", nil); err != nil {
		return err
	}
	missions := make([]any, len(p.Missions))
	for i, m := range p.Missions {
		missions[i] = map[string]any{"tenant_id": s.tenant, "area": m[0], "goal": m[1], "autonomy": 1, "playbook": p.Key}
	}
	if len(missions) > 0 {
		if err := s.do(ctx, http.MethodPost, "missions", nil, missions, "", nil); err != nil {
			return err
		}
	}
	patch := map[string]any{"vertical": p.Key}
	if name != "" {
		patch["name"] = core.Truncate(name, 120)
	}
	return s.do(ctx, http.MethodPatch, "tenants", url.Values{"id": {"eq." + s.tenant}}, patch, "", nil)
}
