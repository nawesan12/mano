// Package stt transcribe audios con cualquier endpoint compatible con OpenAI /audio/transcriptions.
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL, Key, Model string
	hc                  *http.Client
}

func New(baseURL, key, model string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, Model: model, hc: &http.Client{Timeout: 120 * time.Second}}
}

var ErrDisabled = errors.New("la transcripción de audios no está configurada (STT_BASE_URL)")

// Transcribe devuelve el texto del audio. filename define el formato para el proveedor (nota.ogg, nota.mp3…).
func (c *Client) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	if c == nil {
		return "", ErrDisabled
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", filename)
	_, _ = fw.Write(audio)
	_ = w.WriteField("model", c.Model)
	_ = w.WriteField("language", "es")
	_ = w.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("transcripción falló: %d", res.StatusCode)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Text), nil
}
