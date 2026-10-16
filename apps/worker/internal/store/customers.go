package store

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
)

// Customer es un cliente del negocio. Se arma solo con lo que entra: mensajes, mails y pagos.
type Customer struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Phone         string     `json:"phone"`
	Email         string     `json:"email"`
	Contacts      int        `json:"contacts"`
	PaidTotal     float64    `json:"paid_total"`
	LastContactAt *time.Time `json:"last_contact_at"`
	LastChannel   string     `json:"last_channel"`
	Notes         string     `json:"notes"`
}

const customerCols = "id,name,phone,email,contacts,paid_total,last_contact_at,last_channel,notes"

// Touch es lo que se aprende de un evento.
type Touch struct {
	Name, Phone, Email, Channel string
	Paid                        float64
	At                          time.Time
}

func (s *Store) customerBy(ctx context.Context, col, val string) (*Customer, error) {
	var out []Customer
	if err := s.do(ctx, http.MethodGet, "customers", s.q(col, "eq."+val, "select", customerCols, "limit", "1"), nil, "", &out); err != nil || len(out) == 0 {
		return nil, err
	}
	return &out[0], nil
}

// TouchCustomer suma un contacto (o un pago) al cliente; lo crea si no existe. Lo encuentra por teléfono o por mail.
func (s *Store) TouchCustomer(ctx context.Context, t Touch) error {
	t.Email = strings.ToLower(strings.TrimSpace(t.Email))
	if t.Phone == "" && t.Email == "" {
		return nil
	}
	var c *Customer
	var err error
	if t.Phone != "" {
		c, err = s.customerBy(ctx, "phone", t.Phone)
	}
	if c == nil && err == nil && t.Email != "" {
		c, err = s.customerBy(ctx, "email", t.Email)
	}
	if err != nil {
		return err
	}
	at := t.At.UTC().Format(time.RFC3339)
	if c == nil {
		return s.do(ctx, http.MethodPost, "customers", nil, map[string]any{
			"tenant_id": s.tenant, "name": core.Truncate(t.Name, 120), "phone": nullable(t.Phone), "email": nullable(t.Email),
			"contacts": 1, "paid_total": t.Paid, "first_seen_at": at, "last_contact_at": at, "last_channel": t.Channel,
		}, "", nil)
	}
	patch := map[string]any{"contacts": c.Contacts + 1, "paid_total": c.PaidTotal + t.Paid, "last_contact_at": at, "last_channel": t.Channel}
	if c.Name == "" && t.Name != "" {
		patch["name"] = core.Truncate(t.Name, 120)
	}
	if c.Phone == "" && t.Phone != "" {
		patch["phone"] = t.Phone
	}
	if c.Email == "" && t.Email != "" {
		patch["email"] = t.Email
	}
	return s.do(ctx, http.MethodPatch, "customers", s.q("id", "eq."+c.ID), patch, "", nil)
}

// Customers devuelve los clientes (hasta 1000), los más recientes primero.
func (s *Store) Customers(ctx context.Context) ([]Customer, error) {
	var out []Customer
	return out, s.do(ctx, http.MethodGet, "customers", s.q("select", customerCols, "order", "last_contact_at.desc.nullslast", "limit", "1000"), nil, "", &out)
}

func (s *Store) CustomerByPhoneOrEmail(ctx context.Context, phone, email string) (*Customer, error) {
	if phone != "" {
		if c, err := s.customerBy(ctx, "phone", phone); c != nil || err != nil {
			return c, err
		}
	}
	if email != "" {
		return s.customerBy(ctx, "email", strings.ToLower(email))
	}
	return nil, nil
}

// AddCustomerNote suma una nota con fecha. Las notas son del dueño: van tal cual.
func (s *Store) AddCustomerNote(ctx context.Context, c Customer, note string, at time.Time) error {
	line := at.Format("02/01") + " " + core.Truncate(strings.TrimSpace(note), 300)
	notes := line
	if c.Notes != "" {
		notes = c.Notes + "\n" + line
	}
	return s.do(ctx, http.MethodPatch, "customers", s.q("id", "eq."+c.ID), map[string]any{"notes": core.Truncate(notes, 4000)}, "", nil)
}
