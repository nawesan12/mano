package store

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
)

func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// AddChunk guarda un fragmento con su vector para buscarlo después por significado.
func (s *Store) AddChunk(ctx context.Context, source, ref, content string, vec []float32) error {
	row := map[string]any{"tenant_id": s.tenant, "source": source, "ref": nullable(ref), "content": core.Truncate(content, 4000)}
	if vec != nil {
		row["embedding"] = vectorLiteral(vec)
	}
	return s.do(ctx, http.MethodPost, "memory_chunks", nil, row, "", nil)
}

// Chunk es un recuerdo encontrado.
type Chunk struct {
	Source     string    `json:"source"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
	Similarity float64   `json:"similarity"`
}

// MatchChunks busca los fragmentos más parecidos (función match_memory, pgvector).
func (s *Store) MatchChunks(ctx context.Context, vec []float32, k int) ([]Chunk, error) {
	var out []Chunk
	body := map[string]any{"query_embedding": vectorLiteral(vec), "match_tenant": s.tenant, "match_count": k}
	return out, s.do(ctx, http.MethodPost, "rpc/match_memory", nil, body, "", &out)
}

// SearchEvents busca por palabras en lo que entró (respaldo sin embeddings). words ya viene limpio.
func (s *Store) SearchEvents(ctx context.Context, words []string, limit int) ([]Chunk, error) {
	if len(words) == 0 {
		return nil, nil
	}
	var parts []string
	for _, w := range words {
		parts = append(parts, "subject.ilike.*"+w+"*", "body.ilike.*"+w+"*", "sender.ilike.*"+w+"*")
	}
	var rows []struct {
		Source     string    `json:"source"`
		Sender     string    `json:"sender"`
		Subject    string    `json:"subject"`
		Body       string    `json:"body"`
		ReceivedAt time.Time `json:"received_at"`
	}
	q := s.q("select", "source,sender,subject,body,received_at", "or", "("+strings.Join(parts, ",")+")",
		"urgency", "neq.ignore", "order", "received_at.desc", "limit", strconv.Itoa(limit))
	if err := s.do(ctx, http.MethodGet, "events", q, nil, "", &rows); err != nil {
		return nil, err
	}
	out := make([]Chunk, 0, len(rows))
	for _, r := range rows {
		out = append(out, Chunk{Source: r.Source, CreatedAt: r.ReceivedAt,
			Content: "De: " + r.Sender + " · Asunto: " + r.Subject + "\n" + core.Truncate(r.Body, 1200)})
	}
	return out, nil
}
