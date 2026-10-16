// Package tts convierte texto en una nota de voz con cualquier endpoint compatible con OpenAI /audio/speech.
// Devuelve Ogg/Opus, el formato de nota de voz de Telegram y WhatsApp.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nawesan12/mano/worker/internal/core"
)

type Client struct {
	BaseURL, Key, model, Voice string
	PricePerMChars             float64 // USD por millón de caracteres
	hc                         *http.Client
}

// New devuelve nil si no hay endpoint: las notas de voz quedan apagadas.
func New(baseURL, key, model, voice string, pricePerMChars float64) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, model: model, Voice: voice, PricePerMChars: pricePerMChars,
		hc: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) Model() string { return c.model }

// Estimate es el costo de decir text.
func (c *Client) Estimate(text string) float64 {
	return float64(utf8.RuneCountInString(text)) * c.PricePerMChars / 1e6
}

// Synthesize devuelve el audio Ogg/Opus y su costo.
func (c *Client) Synthesize(ctx context.Context, text string) ([]byte, float64, error) {
	text = core.Truncate(strings.TrimSpace(text), 600)
	body, _ := json.Marshal(map[string]string{
		"model": c.model, "input": text, "voice": c.Voice, "response_format": "opus",
		"instructions": "Hablá en español rioplatense, cercano y claro, como un asistente de confianza. Sin apuro.",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	audio, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("tts %d: %s", res.StatusCode, core.Truncate(string(audio), 200))
	}
	if len(audio) == 0 {
		return nil, 0, fmt.Errorf("tts devolvió un audio vacío")
	}
	return audio, c.Estimate(text), nil
}
