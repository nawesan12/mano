package tts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSynthesize(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte("OggS-audio"))
	}))
	defer srv.Close()

	if New("", "k", "m", "v", 15) != nil {
		t.Fatal("sin endpoint queda apagado")
	}
	c := New(srv.URL+"/v1/", "k", "gpt-4o-mini-tts", "marin", 15)
	audio, cost, err := c.Synthesize(context.Background(), "  Hola, soy Mano  ")
	if err != nil || string(audio) != "OggS-audio" {
		t.Fatal(audio, err)
	}
	if got["response_format"] != "opus" || got["input"] != "Hola, soy Mano" || got["voice"] != "marin" {
		t.Fatalf("pedido: %v", got)
	}
	if cost != 14*15/1e6 {
		t.Fatalf("costo por caracteres: %v", cost)
	}
}
