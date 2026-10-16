package store

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
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
	Credits                                                   string // id de la factura que anula (notas de crédito)
	Number, CbteTipo                                          int
	Total                                                     float64
}

func (s *Store) InsertInvoice(ctx context.Context, r InvoiceRow) error {
	return s.do(ctx, http.MethodPost, "invoices", nil, map[string]any{
		"tenant_id": s.tenant, "label": r.Label, "cbte_tipo": r.CbteTipo, "number": r.Number, "cae": r.CAE, "cae_expires": r.CAEExpires,
		"customer": r.Customer, "concept": r.Concept, "total": r.Total, "payment_ref": nullable(r.PaymentRef), "qr_url": r.QR, "credits": nullable(r.Credits),
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

// Invoiced es una factura emitida, para poder anularla.
type Invoiced struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	CbteTipo   int       `json:"cbte_tipo"`
	Number     int       `json:"number"`
	Customer   string    `json:"customer"`
	Total      float64   `json:"total"`
	PaymentRef string    `json:"payment_ref"`
	CreatedAt  time.Time `json:"created_at"`
}

const invoicedCols = "id,label,cbte_tipo,number,customer,total,payment_ref,created_at"

func (s *Store) invoiceWhere(ctx context.Context, kv ...string) (*Invoiced, error) {
	var out []Invoiced
	if err := s.do(ctx, http.MethodGet, "invoices", s.q(append(kv, "select", invoicedCols)...), nil, "", &out); err != nil || len(out) == 0 {
		return nil, err
	}
	return &out[0], nil
}

func (s *Store) InvoiceByPayment(ctx context.Context, ref string) (*Invoiced, error) {
	return s.invoiceWhere(ctx, "payment_ref", "eq."+ref, "cbte_tipo", "eq.11")
}

func (s *Store) InvoiceByNumber(ctx context.Context, cbteTipo, number int) (*Invoiced, error) {
	return s.invoiceWhere(ctx, "cbte_tipo", "eq."+strconv.Itoa(cbteTipo), "number", "eq."+strconv.Itoa(number))
}

// CreditedFor suma lo que ya se anuló de una factura con notas de crédito.
func (s *Store) CreditedFor(ctx context.Context, invoiceID string) (float64, error) {
	var out []struct {
		Total float64 `json:"total"`
	}
	err := s.do(ctx, http.MethodGet, "invoices", s.q("credits", "eq."+invoiceID, "select", "total"), nil, "", &out)
	sum := 0.0
	for _, r := range out {
		sum += r.Total
	}
	return sum, err
}
