package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Decider ejecuta una decisión del panel por el mismo camino que los botones (agent.Agent.OnDecision).
type Decider func(ctx context.Context, approvalID, decision string) string

// PanelAPI recibe las decisiones del panel web. Autenticado con un token compartido (Bearer), solo POST.
type PanelAPI struct {
	Token  string
	Decide Decider
}

var decisions = map[string]bool{"approve": true, "reject": true, "ignore-sender": true}

func (p *PanelAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if p.Token == "" || !ok || subtle.ConstantTimeCompare([]byte(tok), []byte(p.Token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var in struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in) != nil || len(in.ID) != 36 || !decisions[in.Decision] {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": p.Decide(ctx, in.ID, in.Decision)})
}
