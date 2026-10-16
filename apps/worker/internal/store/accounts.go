package store

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// KnownAccount es una cuenta bancaria que un remitente ya usó. trusted = la primera que vimos, o una que el dueño confirmó.
type KnownAccount struct {
	Account string `json:"account"`
	Trusted bool   `json:"trusted"`
}

func (s *Store) KnownAccounts(ctx context.Context, sender string) ([]KnownAccount, error) {
	var out []KnownAccount
	return out, s.do(ctx, http.MethodGet, "known_accounts", s.q("sender", "eq."+sender, "select", "account,trusted"), nil, "", &out)
}

func (s *Store) AddKnownAccount(ctx context.Context, sender, account string, trusted bool) error {
	row := map[string]any{"tenant_id": s.tenant, "sender": sender, "account": account, "trusted": trusted, "seen_at": time.Now().UTC().Format(time.RFC3339)}
	return s.do(ctx, http.MethodPost, "known_accounts", url.Values{"on_conflict": {"tenant_id,sender,account"}}, []any{row}, "resolution=merge-duplicates", nil)
}
