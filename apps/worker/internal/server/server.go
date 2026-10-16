// Package server recibe webhooks (Gmail por Pub/Sub) y expone /health.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
)

type GmailSource interface {
	NewMessagesSince(ctx context.Context, historyID uint64) ([]string, error)
	ToEvent(ctx context.Context, id, tenantID string) (core.Event, error)
}

type EventSink interface {
	OnEvent(ctx context.Context, ev core.Event) error
}

type Server struct {
	Gmail     GmailSource // nil si Gmail no está conectado
	Sink      EventSink
	PushToken string
	TenantID  string
	Base      context.Context
	WhatsApp  http.Handler // nil si WhatsApp no está conectado
	// Otros webhooks (Mercado Pago, Calendar…): ruta → handler. Cada uno valida su firma.
	Hooks map[string]http.Handler

	gmailMu sync.Mutex // procesa los push de a uno: el historyId es secuencial
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"gmail":` + strconv.FormatBool(s.Gmail != nil) + `,"whatsapp":` + strconv.FormatBool(s.WhatsApp != nil) + `}`))
	})
	mux.HandleFunc("POST /webhooks/gmail", s.gmailPush)
	if s.WhatsApp != nil {
		mux.Handle("/webhooks/whatsapp", s.WhatsApp)
	}
	for path, h := range s.Hooks {
		mux.Handle(path, h)
	}
	return mux
}

// gmailPush responde rápido (Pub/Sub reintenta si tardás) y procesa en segundo plano.
func (s *Server) gmailPush(w http.ResponseWriter, r *http.Request) {
	if s.Gmail == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.PushToken == "" || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(s.PushToken)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body struct {
		Message struct {
			Data string `json:"data"`
		} `json:"message"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
	raw, err := base64.StdEncoding.DecodeString(body.Message.Data)
	var data struct {
		HistoryID json.Number `json:"historyId"`
	}
	if err != nil || json.Unmarshal(raw, &data) != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h, err := strconv.ParseUint(data.HistoryID.String(), 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusNoContent)

	go func() {
		s.gmailMu.Lock()
		defer s.gmailMu.Unlock()
		ctx, cancel := context.WithTimeout(s.Base, 5*time.Minute)
		defer cancel()
		ids, err := s.Gmail.NewMessagesSince(ctx, h)
		if err != nil {
			slog.Error("gmail history", "err", err)
			return
		}
		for _, id := range ids {
			ev, err := s.Gmail.ToEvent(ctx, id, s.TenantID)
			if err != nil {
				slog.Error("gmail message", "id", id, "err", err)
				continue
			}
			if err := s.Sink.OnEvent(ctx, ev); err != nil {
				slog.Error("onEvent", "id", id, "err", err)
			}
		}
	}()
}
