package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type ModelConfig struct {
	Tier     Tier
	Provider string // anthropic, openai, gemini, deepseek… (ver Providers)
	Model    string
	APIKey   string
	BaseURL  string
	PriceIn  float64 // USD por millón de tokens
	PriceOut float64
}

func CostOf(c ModelConfig, in, out float64) float64 {
	return (in*c.PriceIn + out*c.PriceOut) / 1_000_000
}

var httpClient = &http.Client{Timeout: 90 * time.Second}

func NewLLM(c ModelConfig) LLM {
	if c.BaseURL == "" {
		c.BaseURL = Providers[c.Provider]
	}
	if c.BaseURL == "" {
		c.BaseURL = Providers["openai"]
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.Provider == "anthropic" {
		return &anthropic{c}
	}
	return &openaiCompat{c}
}

func postJSON(ctx context.Context, url string, headers map[string]string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		return &HTTPError{Status: res.StatusCode, Msg: fmt.Sprintf("%s respondió %d: %s", url, res.StatusCode, truncate(string(raw), 300))}
	}
	return json.Unmarshal(raw, out)
}

// ── Anthropic ──────────────────────────────────────────────

type anthropic struct{ c ModelConfig }

func (a *anthropic) Tier() Tier    { return a.c.Tier }
func (a *anthropic) Model() string { return a.c.Model }

func (a *anthropic) Complete(ctx context.Context, r Request) (Result, error) {
	system := r.System
	if r.JSON {
		system = strings.TrimSpace(system + "\n\nRespondé solo con JSON válido, sin texto alrededor.")
	}
	body := map[string]any{
		"model":      a.c.Model,
		"max_tokens": orDefault(r.MaxTokens, 800),
		"messages":   r.Messages,
	}
	if system != "" {
		// El system se cachea: instrucciones y memoria estable no se vuelven a cobrar completas.
		body["system"] = []map[string]any{{"type": "text", "text": system, "cache_control": map[string]string{"type": "ephemeral"}}}
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			Input       int `json:"input_tokens"`
			Output      int `json:"output_tokens"`
			CacheCreate int `json:"cache_creation_input_tokens"`
			CacheRead   int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	err := postJSON(ctx, a.c.BaseURL+"/v1/messages", map[string]string{"x-api-key": a.c.APIKey, "anthropic-version": "2023-06-01"}, body, &out)
	if err != nil {
		return Result{}, err
	}
	var sb strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	u := out.Usage
	billedIn := float64(u.Input) + float64(u.CacheCreate)*1.25 + float64(u.CacheRead)*0.1
	return Result{Text: sb.String(), Model: a.c.Model, Tier: a.c.Tier, InputTokens: u.Input + u.CacheCreate + u.CacheRead, OutputTokens: u.Output, CostUSD: CostOf(a.c, billedIn, float64(u.Output))}, nil
}

// ── OpenAI-compatible: DeepSeek, OpenAI, OpenRouter, Ollama (BYOK) ──

type openaiCompat struct{ c ModelConfig }

func (o *openaiCompat) Tier() Tier    { return o.c.Tier }
func (o *openaiCompat) Model() string { return o.c.Model }

func (o *openaiCompat) Complete(ctx context.Context, r Request) (Result, error) {
	msgs := make([]Message, 0, len(r.Messages)+1)
	if r.System != "" {
		msgs = append(msgs, Message{Role: "system", Content: r.System})
	}
	msgs = append(msgs, r.Messages...)
	body := map[string]any{"model": o.c.Model, "max_tokens": orDefault(r.MaxTokens, 800), "messages": msgs}
	if r.JSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	headers := map[string]string{}
	if o.c.APIKey != "" {
		headers["Authorization"] = "Bearer " + o.c.APIKey
	}
	if err := postJSON(ctx, strings.TrimRight(o.c.BaseURL, "/")+"/chat/completions", headers, body, &out); err != nil {
		return Result{}, err
	}
	if len(out.Choices) == 0 {
		return Result{}, errors.New("respuesta vacía del modelo")
	}
	return Result{Text: out.Choices[0].Message.Content, Model: o.c.Model, Tier: o.c.Tier, InputTokens: out.Usage.Prompt, OutputTokens: out.Usage.Completion,
		CostUSD: CostOf(o.c, float64(out.Usage.Prompt), float64(out.Usage.Completion))}, nil
}

// ── utilidades ─────────────────────────────────────────────

var fenced = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// ParseJSON extrae el primer objeto JSON aunque venga con ```json o texto alrededor.
func ParseJSON(text string, v any) error {
	raw := text
	if m := fenced.FindStringSubmatch(text); m != nil {
		raw = m[1]
	}
	i, j := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if i < 0 || j < i {
		return errors.New("la respuesta no trae JSON")
	}
	return json.Unmarshal([]byte(raw[i:j+1]), v)
}

func orDefault(v, d int) int {
	if v > 0 {
		return v
	}
	return d
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Truncate recorta por runas (no rompe tildes).
func Truncate(s string, n int) string { return truncate(s, n) }
