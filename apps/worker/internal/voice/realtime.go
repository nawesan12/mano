package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
)

// Brain conecta la oferta WebRTC con el modelo de voz y devuelve su respuesta SDP.
type Brain interface {
	Connect(ctx context.Context, offer string, session map[string]any) (string, error)
}

// Realtime habla con la API de voz en tiempo real de OpenAI (o una compatible): POST /realtime/calls.
type Realtime struct {
	Base, Key string
	hc        *http.Client
}

func NewRealtime(base, key string) *Realtime {
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &Realtime{Base: strings.TrimRight(base, "/"), Key: key, hc: &http.Client{Timeout: 20 * time.Second}}
}

func (r *Realtime) Connect(ctx context.Context, offer string, session map[string]any) (string, error) {
	sess, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("sdp", offer)
	_ = mw.WriteField("session", string(sess))
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Base+"/realtime/calls", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+r.Key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := r.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("realtime %d: %s", res.StatusCode, core.Truncate(string(raw), 300))
	}
	return string(raw), nil
}
