package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanelAPI(t *testing.T) {
	var got string
	p := &PanelAPI{Token: "secreto", Decide: func(_ context.Context, id, d string) string { got = id + ":" + d; return "Enviado." }}
	id := "00000001-0000-4000-8000-000000000000"
	do := func(auth, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/decide", strings.NewReader(body))
		req.Header.Set("Authorization", auth)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		return rec
	}
	if do("Bearer otro", `{"id":"`+id+`","decision":"approve"}`).Code != http.StatusUnauthorized {
		t.Fatal("token incorrecto")
	}
	if do("Bearer secreto", `{"id":"`+id+`","decision":"borrar"}`).Code != http.StatusBadRequest {
		t.Fatal("decisión desconocida")
	}
	rec := do("Bearer secreto", `{"id":"`+id+`","decision":"approve"}`)
	if rec.Code != http.StatusOK || got != id+":approve" || !strings.Contains(rec.Body.String(), "Enviado.") {
		t.Fatal(rec.Code, got, rec.Body.String())
	}
}
