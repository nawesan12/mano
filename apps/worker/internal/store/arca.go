package store

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/nawesan12/mano/worker/internal/arca"
)

// ArcaTicket implementa arca.TicketStore: el ticket de WSAA sobrevive reinicios.
func (s *Store) ArcaTicket(ctx context.Context) (arca.Ticket, error) {
	var rows []struct {
		Token   string    `json:"token"`
		Sign    string    `json:"sign"`
		Expires time.Time `json:"expires_at"`
	}
	if err := s.do(ctx, http.MethodGet, "arca_ta", s.q("select", "token,sign,expires_at"), nil, "", &rows); err != nil || len(rows) == 0 {
		return arca.Ticket{}, err
	}
	return arca.Ticket{Token: rows[0].Token, Sign: rows[0].Sign, Expires: rows[0].Expires}, nil
}

func (s *Store) SetArcaTicket(ctx context.Context, t arca.Ticket) error {
	row := map[string]any{"tenant_id": s.tenant, "token": t.Token, "sign": t.Sign, "expires_at": t.Expires.UTC().Format(time.RFC3339)}
	return s.do(ctx, http.MethodPost, "arca_ta", url.Values{"on_conflict": {"tenant_id"}}, []any{row}, "resolution=merge-duplicates", nil)
}

type InvoiceRow struct {
	Label, CAE, CAEExpires, Customer, Concept, PaymentRef, QR string
	Number, CbteTipo                                          int
	Total                                                     float64
}

func (s *Store) InsertInvoice(ctx context.Context, r InvoiceRow) error {
	return s.do(ctx, http.MethodPost, "invoices", nil, map[string]any{
		"tenant_id": s.tenant, "label": r.Label, "cbte_tipo": r.CbteTipo, "number": r.Number, "cae": r.CAE, "cae_expires": r.CAEExpires,
		"customer": r.Customer, "concept": r.Concept, "total": r.Total, "payment_ref": nullable(r.PaymentRef), "qr_url": r.QR,
	}, "", nil)
}

// InvoicedPayment dice si ese pago ya se facturó (para no proponerlo dos veces).
func (s *Store) InvoicedPayment(ctx context.Context, ref string) bool {
	var rows []struct {
		CAE string `json:"cae"`
	}
	err := s.do(ctx, http.MethodGet, "invoices", s.q("payment_ref", "eq."+ref, "select", "cae"), nil, "", &rows)
	return err == nil && len(rows) > 0
}
