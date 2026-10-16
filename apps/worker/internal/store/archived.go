package store

import (
	"context"
	"net/http"
	"time"
)

type Archived struct {
	MessageID  string    `json:"message_id"`
	Subject    string    `json:"subject"`
	Sender     string    `json:"sender"`
	ArchivedAt time.Time `json:"archived_at"`
	Restored   bool      `json:"restored"`
}

func (s *Store) AddArchived(ctx context.Context, a Archived) error {
	return s.do(ctx, http.MethodPost, "archived_mail", nil, map[string]any{"tenant_id": s.tenant, "message_id": a.MessageID, "subject": a.Subject,
		"sender": a.Sender, "archived_at": time.Now().UTC().Format(time.RFC3339)}, "", nil)
}

// ArchivedSince devuelve lo archivado y no restaurado desde un momento.
func (s *Store) ArchivedSince(ctx context.Context, since time.Time) ([]Archived, error) {
	var rows []Archived
	q := s.q("select", "message_id,subject,sender,archived_at,restored", "restored", "is.false", "archived_at", "gte."+since.UTC().Format(time.RFC3339), "order", "archived_at.desc", "limit", "500")
	if err := s.do(ctx, http.MethodGet, "archived_mail", q, nil, "", &rows); err != nil {
		return nil, err
	}
	kept := rows[:0]
	for _, r := range rows {
		if !r.Restored && (r.ArchivedAt.IsZero() || !r.ArchivedAt.Before(since)) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

func (s *Store) MarkRestored(ctx context.Context, messageID string) error {
	return s.do(ctx, http.MethodPatch, "archived_mail", s.q("message_id", "eq."+messageID), map[string]any{"restored": true}, "", nil)
}
