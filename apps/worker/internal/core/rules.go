package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RuleMatch y RuleOutcome se guardan como JSON en la tabla rules.
type RuleMatch struct {
	Source            string   `json:"source,omitempty"`
	Kind              string   `json:"kind,omitempty"`
	SenderIncludesAny []string `json:"senderIncludesAny,omitempty"`
	TextIncludesAny   []string `json:"textIncludesAny,omitempty"`
	AmountGte         *float64 `json:"amountGte,omitempty"`
}

type RuleOutcome struct {
	Urgency     Urgency `json:"urgency"`
	NeedsAction bool    `json:"needsAction,omitempty"`
	Note        string  `json:"note,omitempty"`
}

type Rule struct {
	ID       string
	Priority int
	Match    RuleMatch
	Outcome  RuleOutcome
}

func strictUnmarshal(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

// ParseRule valida una fila de la base. Una regla mal formada se descarta.
func ParseRule(id string, priority int, match, outcome []byte) (Rule, error) {
	r := Rule{ID: id, Priority: priority}
	if err := strictUnmarshal(match, &r.Match); err != nil {
		return r, fmt.Errorf("match inválido: %w", err)
	}
	if err := strictUnmarshal(outcome, &r.Outcome); err != nil {
		return r, fmt.Errorf("outcome inválido: %w", err)
	}
	switch r.Outcome.Urgency {
	case Critical, High, Normal, Ignore:
	default:
		return r, fmt.Errorf("urgencia inválida %q", r.Outcome.Urgency)
	}
	m := r.Match
	if m.Source == "" && m.Kind == "" && len(m.SenderIncludesAny) == 0 && len(m.TextIncludesAny) == 0 && m.AmountGte == nil {
		return r, fmt.Errorf("match vacío")
	}
	return r, nil
}

var accents = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
	"à", "a", "è", "e", "ì", "i", "ò", "o", "ù", "u", "â", "a", "ê", "e", "ô", "o", "ã", "a", "õ", "o", "ç", "c",
)

// Norm pasa a minúsculas y saca tildes, para comparar como escribe la gente.
func Norm(s string) string { return accents.Replace(strings.ToLower(s)) }

func containsAny(hay string, needles []string) bool {
	h := Norm(hay)
	for _, n := range needles {
		if strings.Contains(h, Norm(n)) {
			return true
		}
	}
	return false
}

func (r Rule) Matches(ev Event) bool {
	m := r.Match
	if m.Source != "" && m.Source != ev.Source {
		return false
	}
	if m.Kind != "" && m.Kind != ev.Kind {
		return false
	}
	if len(m.SenderIncludesAny) > 0 && !containsAny(ev.Sender, m.SenderIncludesAny) {
		return false
	}
	if len(m.TextIncludesAny) > 0 && !containsAny(ev.Subject+"\n"+ev.Body, m.TextIncludesAny) {
		return false
	}
	if m.AmountGte != nil && (ev.Amount == nil || *ev.Amount < *m.AmountGte) {
		return false
	}
	return true
}

// FirstMatch devuelve la regla de menor prioridad que aplica, o nil.
func FirstMatch(rules []Rule, ev Event) *Rule {
	sorted := append([]Rule(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })
	for i := range sorted {
		if sorted[i].Matches(ev) {
			return &sorted[i]
		}
	}
	return nil
}
