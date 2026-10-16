// Package store habla con Supabase por su API REST (PostgREST) con la service role.
// Fase 0 es mono-tenant: todas las consultas van filtradas por el tenant configurado.
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
)

type Store struct {
	base, key, tenant string
	hc                *http.Client
}

func New(supabaseURL, serviceKey, tenantID string) *Store {
	return &Store{base: supabaseURL + "/rest/v1/", key: serviceKey, tenant: tenantID, hc: &http.Client{Timeout: 20 * time.Second}}
}

func (s *Store) Tenant() string { return s.tenant }

func (s *Store) do(ctx context.Context, method, table string, q url.Values, body any, prefer string, out any) error {
	u := s.base + table
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", s.key)
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/json")
	if prefer != "" {
		req.Header.Set("Prefer", prefer)
	}
	res, err := s.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("supabase %s %s: %d %s", method, table, res.StatusCode, core.Truncate(string(raw), 300))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (s *Store) q(kv ...string) url.Values {
	v := url.Values{"tenant_id": {"eq." + s.tenant}}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

// ── Eventos ────────────────────────────────────────────────

// InsertEvent guarda el evento. inserted=false si ya existía (dedupe por external_id).
func (s *Store) InsertEvent(ctx context.Context, ev core.Event) (id string, inserted bool, err error) {
	payload := ev.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	row := map[string]any{
		"tenant_id": s.tenant, "source": ev.Source, "external_id": ev.ExternalID, "kind": ev.Kind,
		"sender": ev.Sender, "subject": ev.Subject, "body": core.Truncate(ev.Body, 20000),
		"amount": ev.Amount, "payload": payload, "received_at": ev.ReceivedAt.UTC().Format(time.RFC3339),
	}
	var out []struct {
		ID string `json:"id"`
	}
	q := url.Values{"on_conflict": {"tenant_id,source,external_id"}, "select": {"id"}}
	if err = s.do(ctx, http.MethodPost, "events", q, []any{row}, "resolution=ignore-duplicates,return=representation", &out); err != nil {
		return "", false, err
	}
	if len(out) == 0 {
		return "", false, nil
	}
	return out[0].ID, true, nil
}

func (s *Store) SaveTriage(ctx context.Context, eventID string, t core.Triage) error {
	return s.do(ctx, http.MethodPatch, "events", url.Values{"id": {"eq." + eventID}},
		map[string]any{"urgency": t.Urgency, "triaged_via": t.Via, "summary": t.Summary}, "", nil)
}

type BriefItem struct {
	ID      string `json:"id"`
	Urgency string `json:"urgency"`
	Summary string `json:"summary"`
	Source  string `json:"source"`
}

func (s *Store) Unbriefed(ctx context.Context) ([]BriefItem, error) {
	since := time.Now().Add(-36 * time.Hour).UTC().Format(time.RFC3339)
	q := s.q("select", "id,urgency,summary,source", "briefed_at", "is.null", "urgency", "neq.ignore",
		"received_at", "gte."+since, "order", "received_at", "limit", "80")
	var out []BriefItem
	return out, s.do(ctx, http.MethodGet, "events", q, nil, "", &out)
}

func (s *Store) MarkBriefed(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	q := url.Values{"id": {"in.(" + strings.Join(ids, ",") + ")"}}
	return s.do(ctx, http.MethodPatch, "events", q, map[string]any{"briefed_at": time.Now().UTC().Format(time.RFC3339)}, "", nil)
}

// ── Reglas ─────────────────────────────────────────────────

func (s *Store) Rules(ctx context.Context) ([]core.Rule, error) {
	var rows []struct {
		ID       string          `json:"id"`
		Priority int             `json:"priority"`
		Match    json.RawMessage `json:"match"`
		Outcome  json.RawMessage `json:"outcome"`
	}
	if err := s.do(ctx, http.MethodGet, "rules", s.q("select", "id,priority,match,outcome", "active", "is.true"), nil, "", &rows); err != nil {
		return nil, err
	}
	out := make([]core.Rule, 0, len(rows))
	for _, r := range rows {
		if rule, err := core.ParseRule(r.ID, r.Priority, r.Match, r.Outcome); err == nil {
			out = append(out, rule)
		}
	}
	return out, nil
}

func (s *Store) BumpRule(ctx context.Context, id string) error {
	var rows []struct {
		Hits int `json:"hits"`
	}
	if err := s.do(ctx, http.MethodGet, "rules", url.Values{"id": {"eq." + id}, "select": {"hits"}}, nil, "", &rows); err != nil || len(rows) == 0 {
		return err
	}
	return s.do(ctx, http.MethodPatch, "rules", url.Values{"id": {"eq." + id}}, map[string]any{"hits": rows[0].Hits + 1}, "", nil)
}

func (s *Store) AddRule(ctx context.Context, match core.RuleMatch, outcome core.RuleOutcome, origin string) error {
	prio := 80
	if origin == "owner" {
		prio = 50
	}
	return s.do(ctx, http.MethodPost, "rules", nil, map[string]any{"tenant_id": s.tenant, "match": match, "outcome": outcome, "origin": origin, "priority": prio}, "", nil)
}

// ── Gasto ──────────────────────────────────────────────────

func (s *Store) Spend(ctx context.Context) (core.Spend, error) {
	var rows []struct {
		Day   float64 `json:"day_usd"`
		Month float64 `json:"month_usd"`
		Hour  float64 `json:"hour_usd"`
	}
	if err := s.do(ctx, http.MethodGet, "spend_summary", s.q("select", "day_usd,month_usd,hour_usd"), nil, "", &rows); err != nil {
		return core.Spend{}, err
	}
	if len(rows) == 0 {
		return core.Spend{}, nil
	}
	return core.Spend{DayUSD: rows[0].Day, MonthUSD: rows[0].Month, HourUSD: rows[0].Hour}, nil
}

func (s *Store) Caps(ctx context.Context, fallback core.Caps) core.Caps {
	var rows []struct {
		Day   float64 `json:"cap_day_usd"`
		Month float64 `json:"cap_month_usd"`
	}
	if err := s.do(ctx, http.MethodGet, "budgets", s.q("select", "cap_day_usd,cap_month_usd"), nil, "", &rows); err != nil || len(rows) == 0 {
		return fallback
	}
	return core.Caps{DayUSD: rows[0].Day, MonthUSD: rows[0].Month}
}

func (s *Store) RecordUsage(ctx context.Context, r core.Result, taskID string) error {
	if r.CostUSD <= 0 && r.InputTokens == 0 {
		return nil
	}
	return s.do(ctx, http.MethodPost, "usage", nil, map[string]any{
		"tenant_id": s.tenant, "task_id": nullable(taskID), "tier": r.Tier, "model": r.Model,
		"input_tokens": r.InputTokens, "output_tokens": r.OutputTokens, "cost_usd": r.CostUSD,
	}, "", nil)
}

// ── Registro y tareas ──────────────────────────────────────

type LogEntry struct {
	Area, Tool, Summary, Via, Model, ApprovedBy, TaskID string
	CostUSD                                             float64
}

func (s *Store) Log(ctx context.Context, e LogEntry) error {
	if e.ApprovedBy == "" {
		e.ApprovedBy = "auto"
	}
	return s.do(ctx, http.MethodPost, "actions_log", nil, map[string]any{
		"tenant_id": s.tenant, "task_id": nullable(e.TaskID), "area": e.Area, "tool": e.Tool, "summary": e.Summary,
		"resolved_via": e.Via, "model": nullable(e.Model), "approved_by": e.ApprovedBy, "cost_usd": e.CostUSD,
	}, "", nil)
}

type LogRow struct {
	Area    string  `json:"area"`
	Summary string  `json:"summary"`
	CostUSD float64 `json:"cost_usd"`
}

func (s *Store) TodayLog(ctx context.Context, loc *time.Location) ([]LogRow, error) {
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).UTC().Format(time.RFC3339)
	var out []LogRow
	q := s.q("select", "area,summary,cost_usd", "created_at", "gte."+start, "order", "created_at.desc", "limit", "30")
	return out, s.do(ctx, http.MethodGet, "actions_log", q, nil, "", &out)
}

func (s *Store) CreateTask(ctx context.Context, title, eventID string) (string, error) {
	var out []struct {
		ID string `json:"id"`
	}
	err := s.do(ctx, http.MethodPost, "tasks", url.Values{"select": {"id"}},
		map[string]any{"tenant_id": s.tenant, "event_id": nullable(eventID), "title": core.Truncate(title, 140), "state": "classified"},
		"return=representation", &out)
	if err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", errors.New("createTask sin id")
	}
	return out[0].ID, nil
}

func (s *Store) SetTask(ctx context.Context, id, state string, patch map[string]any) error {
	if id == "" {
		return nil
	}
	body := map[string]any{"state": state, "updated_at": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range patch {
		body[k] = v
	}
	return s.do(ctx, http.MethodPatch, "tasks", url.Values{"id": {"eq." + id}}, body, "", nil)
}

// ── Aprobaciones ───────────────────────────────────────────

type Action struct {
	Type      string `json:"type"` // gmail.reply | note
	ThreadID  string `json:"threadId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	To        string `json:"to,omitempty"`
	Subject   string `json:"subject,omitempty"`
	Body      string `json:"body,omitempty"`
	Text      string `json:"text,omitempty"`
}

type Approval struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
	Area   string `json:"area"`
	Risk   string `json:"risk"` // bajo | medio | alto
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Action Action `json:"action"`
	Status string `json:"status"`
}

const approvalCols = "id,task_id,area,risk,title,detail,action,status"

func (s *Store) CreateApproval(ctx context.Context, a Approval) (string, error) {
	var out []struct {
		ID string `json:"id"`
	}
	err := s.do(ctx, http.MethodPost, "approvals", url.Values{"select": {"id"}}, map[string]any{
		"tenant_id": s.tenant, "task_id": nullable(a.TaskID), "area": a.Area, "risk": a.Risk, "title": a.Title,
		"detail": a.Detail, "action": a.Action, "expires_at": time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339),
	}, "return=representation", &out)
	if err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", errors.New("createApproval sin id")
	}
	return out[0].ID, nil
}

// DecideApproval solo actualiza si seguía pendiente: evita doble ejecución por doble toque.
func (s *Store) DecideApproval(ctx context.Context, id, status, by string) (*Approval, error) {
	var out []Approval
	q := s.q("id", "eq."+id, "status", "eq.pending", "select", approvalCols)
	err := s.do(ctx, http.MethodPatch, "approvals", q, map[string]any{"status": status, "decided_by": by, "decided_at": time.Now().UTC().Format(time.RFC3339)}, "return=representation", &out)
	if err != nil || len(out) == 0 {
		return nil, err
	}
	return &out[0], nil
}

func (s *Store) PendingApprovals(ctx context.Context) ([]Approval, error) {
	var out []Approval
	return out, s.do(ctx, http.MethodGet, "approvals", s.q("status", "eq.pending", "select", approvalCols, "order", "created_at"), nil, "", &out)
}

// ── Memoria y misiones ─────────────────────────────────────

type Fact struct {
	Topic string `json:"topic"`
	Fact  string `json:"fact"`
}

func (s *Store) Facts(ctx context.Context) ([]Fact, error) {
	var out []Fact
	return out, s.do(ctx, http.MethodGet, "memory_facts", s.q("select", "topic,fact", "order", "updated_at.desc", "limit", "40"), nil, "", &out)
}

func (s *Store) AddFact(ctx context.Context, topic, fact string) error {
	return s.do(ctx, http.MethodPost, "memory_facts", url.Values{"on_conflict": {"tenant_id,topic,fact"}},
		map[string]any{"tenant_id": s.tenant, "topic": topic, "fact": fact, "updated_at": time.Now().UTC().Format(time.RFC3339)},
		"resolution=merge-duplicates", nil)
}

func (s *Store) ForgetTopic(ctx context.Context, topic string) (int, error) {
	var out []struct {
		ID string `json:"id"`
	}
	err := s.do(ctx, http.MethodDelete, "memory_facts", s.q("topic", "ilike."+topic, "select", "id"), nil, "return=representation", &out)
	return len(out), err
}

type Mission struct {
	Area     string `json:"area"`
	Goal     string `json:"goal"`
	Autonomy int    `json:"autonomy"`
}

func (s *Store) Missions(ctx context.Context) ([]Mission, error) {
	var out []Mission
	return out, s.do(ctx, http.MethodGet, "missions", s.q("select", "area,goal,autonomy", "active", "is.true"), nil, "", &out)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ── WhatsApp ───────────────────────────────────────────────

// MarkProcessed registra el id del evento entrante. false = ya se había procesado (reintento de Meta).
func (s *Store) MarkProcessed(ctx context.Context, messageID string) (bool, error) {
	var out []struct {
		ID string `json:"message_id"`
	}
	q := url.Values{"on_conflict": {"message_id"}, "select": {"message_id"}}
	err := s.do(ctx, http.MethodPost, "processed_events", q, []any{map[string]any{"message_id": messageID, "tenant_id": s.tenant}},
		"resolution=ignore-duplicates,return=representation", &out)
	return len(out) > 0, err
}

// TouchContact actualiza la última vez que el contacto escribió: abre la ventana de 24 h.
func (s *Store) TouchContact(ctx context.Context, phone, name string, isOwner bool, at time.Time) error {
	row := map[string]any{"tenant_id": s.tenant, "phone": phone, "is_owner": isOwner, "last_inbound_at": at.UTC().Format(time.RFC3339)}
	if name != "" {
		row["name"] = name
	}
	return s.do(ctx, http.MethodPost, "wa_contacts", url.Values{"on_conflict": {"tenant_id,phone"}}, []any{row}, "resolution=merge-duplicates", nil)
}

// LastInbound devuelve cuándo escribió por última vez el contacto (cero si nunca).
func (s *Store) LastInbound(ctx context.Context, phone string) (time.Time, error) {
	var rows []struct {
		At *time.Time `json:"last_inbound_at"`
	}
	if err := s.do(ctx, http.MethodGet, "wa_contacts", s.q("phone", "eq."+phone, "select", "last_inbound_at"), nil, "", &rows); err != nil {
		return time.Time{}, err
	}
	if len(rows) == 0 || rows[0].At == nil {
		return time.Time{}, nil
	}
	return *rows[0].At, nil
}

func (s *Store) RecordOutbound(ctx context.Context, wamid, phone, kind, template string) error {
	return s.do(ctx, http.MethodPost, "wa_messages", nil, map[string]any{
		"wamid": wamid, "tenant_id": s.tenant, "phone": phone, "kind": kind, "template": nullable(template),
	}, "", nil)
}

// UpdateMessageStatus guarda lo que informa Meta: estado, categoría facturada y si se cobró.
func (s *Store) UpdateMessageStatus(ctx context.Context, wamid, status, category string, billable *bool, errText string) error {
	patch := map[string]any{"status": status, "updated_at": time.Now().UTC().Format(time.RFC3339)}
	if category != "" {
		patch["category"] = category
	}
	if billable != nil {
		patch["billable"] = *billable
	}
	if errText != "" {
		patch["error"] = core.Truncate(errText, 500)
	}
	return s.do(ctx, http.MethodPatch, "wa_messages", s.q("wamid", "eq."+wamid), patch, "", nil)
}

// ── Seguimiento ────────────────────────────────────────────

// OpenApproval es un pendiente con sus tiempos, para recordar y vencer.
type OpenApproval struct {
	Approval
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RemindedAt *time.Time `json:"reminded_at"`
}

func (s *Store) OpenApprovals(ctx context.Context) ([]OpenApproval, error) {
	var out []OpenApproval
	q := s.q("status", "eq.pending", "select", approvalCols+",created_at,expires_at,reminded_at", "order", "created_at")
	return out, s.do(ctx, http.MethodGet, "approvals", q, nil, "", &out)
}

func (s *Store) MarkReminded(ctx context.Context, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	q := s.q("id", "in.("+strings.Join(ids, ",")+")")
	return s.do(ctx, http.MethodPatch, "approvals", q, map[string]any{"reminded_at": at.UTC().Format(time.RFC3339)}, "", nil)
}

// ExpireApproval vence un pendiente solo si sigue pendiente (mismo cuidado que DecideApproval).
func (s *Store) ExpireApproval(ctx context.Context, id string) (bool, error) {
	var out []struct {
		ID string `json:"id"`
	}
	q := s.q("id", "eq."+id, "status", "eq.pending", "select", "id")
	err := s.do(ctx, http.MethodPatch, "approvals", q, map[string]any{"status": "expired", "decided_by": "vencida", "decided_at": time.Now().UTC().Format(time.RFC3339)}, "return=representation", &out)
	return len(out) > 0, err
}
