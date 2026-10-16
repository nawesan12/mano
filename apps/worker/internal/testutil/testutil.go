// Package testutil trae dobles de prueba compartidos: un PostgREST en memoria y modelos guionados.
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/nawesan12/mano/worker/internal/core"
)

// FakeRest imita lo justo de PostgREST: filtros eq/neq/is/ilike/in, insert, upsert
// (on_conflict + ignore o merge), patch y delete.
type FakeRest struct {
	mu     sync.Mutex
	Tables map[string][]map[string]any
	seq    int
}

func NewFakeRest(seed map[string][]map[string]any) *FakeRest {
	if seed == nil {
		seed = map[string][]map[string]any{}
	}
	return &FakeRest{Tables: seed}
}

func (f *FakeRest) Rows(table string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.Tables[table]...)
}

func (f *FakeRest) Set(table string, rows []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Tables[table] = rows
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func (f *FakeRest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	table := strings.TrimPrefix(r.URL.Path, "/rest/v1/")
	q := r.URL.Query()
	match := func(row map[string]any) bool {
		for k, vs := range q {
			switch k {
			case "select", "order", "limit", "on_conflict":
				continue
			}
			v, cur := vs[0], str(row[k])
			switch {
			case strings.HasPrefix(v, "eq.") && cur != v[3:]:
				return false
			case strings.HasPrefix(v, "neq.") && cur == v[4:]:
				return false
			case v == "is.null" && cur != "":
				return false
			case v == "is.true" && row[k] != true:
				return false
			case strings.HasPrefix(v, "ilike.") && !strings.EqualFold(cur, v[6:]):
				return false
			case strings.HasPrefix(v, "in.(") && !strings.Contains(v, cur):
				return false
			}
		}
		return true
	}
	var out []map[string]any
	body, _ := io.ReadAll(r.Body)
	if strings.HasPrefix(table, "rpc/") {
		// funciones: devuelven lo que el test cargó en Tables["rpc/<nombre>"]
		out = append(out, f.Tables[table]...)
		if out == nil {
			out = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	switch r.Method {
	case http.MethodGet:
		for _, row := range f.Tables[table] {
			if match(row) {
				out = append(out, row)
			}
		}
	case http.MethodPost:
		var rows []map[string]any
		if err := json.Unmarshal(body, &rows); err != nil {
			var one map[string]any
			_ = json.Unmarshal(body, &one)
			rows = []map[string]any{one}
		}
		conflict := strings.Split(q.Get("on_conflict"), ",")
		merge := strings.Contains(r.Header.Get("Prefer"), "merge-duplicates")
	rowLoop:
		for _, row := range rows {
			if q.Get("on_conflict") != "" {
				for _, e := range f.Tables[table] {
					same := true
					for _, c := range conflict {
						if str(e[c]) != str(row[c]) {
							same = false
						}
					}
					if same {
						if merge {
							for k, v := range row {
								e[k] = v
							}
							out = append(out, e)
						}
						continue rowLoop
					}
				}
			}
			if _, ok := row["id"]; !ok {
				f.seq++
				row["id"] = fmt.Sprintf("%08d-0000-4000-8000-000000000000", f.seq)
			}
			switch table {
			case "rules":
				row["active"] = true
			case "approvals":
				row["status"] = "pending"
			}
			f.Tables[table] = append(f.Tables[table], row)
			out = append(out, row)
		}
	case http.MethodPatch:
		var patch map[string]any
		_ = json.Unmarshal(body, &patch)
		for _, row := range f.Tables[table] {
			if match(row) {
				for k, v := range patch {
					row[k] = v
				}
				out = append(out, row)
			}
		}
	case http.MethodDelete:
		keep := f.Tables[table][:0]
		for _, row := range f.Tables[table] {
			if match(row) {
				out = append(out, row)
			} else {
				keep = append(keep, row)
			}
		}
		f.Tables[table] = keep
	}
	if out == nil {
		out = []map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(out)
}

// ScriptedLLM devuelve lo que diga Reply y cuenta las llamadas.
type ScriptedLLM struct {
	T     core.Tier
	Reply func(core.Request) string
	mu    sync.Mutex
	Calls int
}

func (s *ScriptedLLM) Tier() core.Tier { return s.T }
func (s *ScriptedLLM) Model() string   { return string(s.T) + "-model" }
func (s *ScriptedLLM) Complete(_ context.Context, r core.Request) (core.Result, error) {
	s.mu.Lock()
	s.Calls++
	s.mu.Unlock()
	return core.Result{Text: s.Reply(r), Tier: s.T, Model: s.Model(), InputTokens: 100, OutputTokens: 50, CostUSD: 0.01}, nil
}

const Tenant = "00000000-0000-0000-0000-000000000001"
