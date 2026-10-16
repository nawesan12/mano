package gmail

import (
	"context"
	"encoding/base64"
	"github.com/nawesan12/mano/worker/internal/google"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func b64(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

func TestExtractText(t *testing.T) {
	p := &Part{MimeType: "multipart/alternative", Parts: []Part{
		{MimeType: "text/html"},
		{MimeType: "text/plain"},
	}}
	p.Parts[0].Body.Data = b64("<p>Hola <b>HTML</b></p>")
	p.Parts[1].Body.Data = b64("Hola plano ñandú")
	if got := ExtractText(p); got != "Hola plano ñandú" {
		t.Fatalf("prefiere text/plain: %q", got)
	}
	h := &Part{MimeType: "text/html"}
	h.Body.Data = b64("<style>x{}</style><p>Pago&nbsp;recibido</p><br>Gracias")
	if got := ExtractText(h); got != "Pago recibido\nGracias" {
		t.Fatalf("limpia HTML: %q", got)
	}
}

func TestEmailOf(t *testing.T) {
	if EmailOf("Marta Pérez <marta@costaazul.com>") != "marta@costaazul.com" || EmailOf(" solo@mail.com ") != "solo@mail.com" {
		t.Fatal("EmailOf")
	}
}

func TestHistoryFallbackAndTokenCache(t *testing.T) {
	tokenCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			tokenCalls++
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
		case r.Header.Get("Authorization") != "Bearer tok":
			w.WriteHeader(401)
		case strings.HasSuffix(r.URL.Path, "/history"):
			w.WriteHeader(404) // historyId vencido
		case strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = w.Write([]byte(`{"messages":[{"id":"a"},{"id":"b"},{"id":"a"}]}`))
		}
	}))
	defer srv.Close()
	c := New(google.NewAuth("id", "secret", "refresh"), "topic")
	c.auth.TokenURL, c.base = srv.URL+"/token", srv.URL+"/gmail/"
	ids, err := c.NewMessagesSince(context.Background(), 100)
	if err != nil || strings.Join(ids, ",") != "a,b" {
		t.Fatalf("fallback: %v %v", ids, err)
	}
	_, _ = c.NewMessagesSince(context.Background(), 101)
	if tokenCalls != 1 {
		t.Fatalf("el token se debe cachear, pedidos: %d", tokenCalls)
	}
}
