// Package core tiene la lógica pura de Mano: reglas, cascada de triage,
// gobernador de gasto y clientes de IA. No toca base de datos ni red salvo la IA.
package core

import (
	"context"
	"time"
)

type Urgency string

const (
	Critical Urgency = "critical"
	High     Urgency = "high"
	Normal   Urgency = "normal"
	Ignore   Urgency = "ignore"
)

type Tier string

const (
	Cheap Tier = "cheap"
	Top   Tier = "top"
)

// Event es todo lo que entra a Mano, normalizado.
type Event struct {
	TenantID   string
	Source     string // gmail, telegram, whatsapp, calendar, mercadopago, cron
	ExternalID string
	Kind       string
	Sender     string
	Subject    string
	Body       string
	Amount     *float64
	ReceivedAt time.Time
	Payload    map[string]any
}

type Triage struct {
	Urgency     Urgency
	NeedsAction bool
	Intent      string
	Summary     string
	Via         string // rule | classifier
	RuleID      string
	CostUSD     float64
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	System    string
	Messages  []Message
	MaxTokens int
	JSON      bool // pide JSON puro
}

type Result struct {
	Text         string
	Model        string
	Tier         Tier
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

type LLM interface {
	Tier() Tier
	Model() string
	Complete(ctx context.Context, req Request) (Result, error)
}
