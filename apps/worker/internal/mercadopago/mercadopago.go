// Package mercadopago recibe las notificaciones de pagos (webhook firmado) y las convierte en eventos.
// Un cobro grande, un pago rechazado o un contracargo entran a la cascada como cualquier mail.
package mercadopago

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

type Client struct {
	token, base string
	hc          *http.Client
}

func NewClient(token string) *Client {
	return &Client{token: token, base: "https://api.mercadopago.com", hc: &http.Client{Timeout: 20 * time.Second}}
}

type Payment struct {
	ID                int64   `json:"id"`
	Status            string  `json:"status"` // approved | rejected | refunded | charged_back | cancelled | pending | in_process
	StatusDetail      string  `json:"status_detail"`
	TransactionAmount float64 `json:"transaction_amount"`
	CurrencyID        string  `json:"currency_id"`
	Description       string  `json:"description"`
	ExternalReference string  `json:"external_reference"`
	DateCreated       string  `json:"date_created"`
	DateApproved      string  `json:"date_approved"`
	Payer             struct {
		Email     string `json:"email"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	} `json:"payer"`
}

func (c *Client) Payment(ctx context.Context, id string) (Payment, error) {
	var p Payment
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/payments/"+id, nil)
	if err != nil {
		return p, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.hc.Do(req)
	if err != nil {
		return p, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return p, fmt.Errorf("mercadopago %d: %s", res.StatusCode, core.Truncate(string(raw), 200))
	}
	return p, json.Unmarshal(raw, &p)
}

var statusLabel = map[string]string{
	"approved": "acreditado", "rejected": "rechazado", "refunded": "devuelto", "charged_back": "con contracargo", "cancelled": "cancelado",
}

// Relevant: los estados intermedios (pending, in_process) no generan eventos; llega otro aviso cuando se resuelven.
func Relevant(status string) bool { _, ok := statusLabel[status]; return ok }

// ToEvent normaliza el pago. El id incluye el estado: un mismo pago puede acreditarse y después tener contracargo.
func ToEvent(p Payment, tenantID string) core.Event {
	name := strings.TrimSpace(p.Payer.FirstName + " " + p.Payer.LastName)
	sender := p.Payer.Email
	if name != "" && sender != "" {
		sender = name + " <" + sender + ">"
	} else if name != "" {
		sender = name
	}
	subject := p.Description
	if subject == "" {
		subject = "Pago " + strconv.FormatInt(p.ID, 10)
	}
	body := fmt.Sprintf("Pago %s de %s %s.", statusLabel[p.Status], p.CurrencyID, money(p.TransactionAmount))
	if p.StatusDetail != "" && p.Status != "approved" {
		body += " Motivo: " + p.StatusDetail + "."
	}
	if p.ExternalReference != "" {
		body += " Referencia: " + p.ExternalReference + "."
	}
	at := time.Now()
	for _, s := range []string{p.DateApproved, p.DateCreated} {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			at = t
			break
		}
	}
	amount := p.TransactionAmount
	return core.Event{
		TenantID: tenantID, Source: "mercadopago", ExternalID: strconv.FormatInt(p.ID, 10) + ":" + p.Status, Kind: "payment." + p.Status,
		Sender: sender, Subject: subject, Body: body, Amount: &amount, ReceivedAt: at,
		Payload: map[string]any{"paymentId": p.ID, "status": p.Status, "currency": p.CurrencyID, "reference": p.ExternalReference},
	}
}

// money: 420000.5 → "420.000,50" (formato argentino, sin depender de locale).
func money(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	ent, dec, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range ent {
		if i > 0 && (len(ent)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if dec == "00" {
		return b.String()
	}
	return b.String() + "," + dec
}

// ValidSignature valida x-signature ("ts=…,v1=…") con el manifiesto de Mercado Pago:
// "id:<data.id>;request-id:<x-request-id>;ts:<ts>;" (se omite lo que no venga). data.id va en minúsculas.
func ValidSignature(secret, header, requestID, dataID string) bool {
	if secret == "" || header == "" {
		return false
	}
	var ts, v1 string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "ts":
			ts = v
		case "v1":
			v1 = v
		}
	}
	if ts == "" || v1 == "" {
		return false
	}
	var m strings.Builder
	if dataID != "" {
		m.WriteString("id:" + strings.ToLower(dataID) + ";")
	}
	if requestID != "" {
		m.WriteString("request-id:" + requestID + ";")
	}
	m.WriteString("ts:" + ts + ";")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(m.String()))
	want, err := hex.DecodeString(v1)
	return err == nil && hmac.Equal(want, mac.Sum(nil))
}

// EventSink recibe los pagos como eventos (agent.Agent).
type EventSink interface {
	OnEvent(ctx context.Context, ev core.Event) error
}

// Charges marca como pagados los links que mandó Mano (store.Store).
type Charges interface {
	MarkChargePaid(ctx context.Context, id string, paymentID int64) (*store.Charge, error)
}

type Webhook struct {
	Client   *Client
	Charges  Charges // puede ser nil
	Secret   string
	Sink     EventSink
	TenantID string
	Base     context.Context
}

func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var n struct {
		Type string `json:"type"`
		Data struct {
			ID any `json:"id"` // a veces string, a veces número
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &n)
	dataID := r.URL.Query().Get("data.id")
	if dataID == "" && n.Data.ID != nil {
		dataID = fmt.Sprint(n.Data.ID)
	}
	if !ValidSignature(w.Secret, r.Header.Get("x-signature"), r.Header.Get("x-request-id"), dataID) {
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}
	rw.WriteHeader(http.StatusOK) // Mercado Pago reintenta si no contestamos en 22 s
	typ := n.Type
	if typ == "" {
		typ = r.URL.Query().Get("type")
	}
	if typ != "payment" || dataID == "" {
		return
	}
	go w.process(dataID)
}

func (w *Webhook) process(id string) {
	ctx, cancel := context.WithTimeout(w.Base, 2*time.Minute)
	defer cancel()
	p, err := w.Client.Payment(ctx, id)
	if err != nil {
		slog.Error("mercadopago pago", "err", err)
		return
	}
	if !Relevant(p.Status) {
		return
	}
	ev := ToEvent(p, w.TenantID)
	if p.Status == "approved" && strings.HasPrefix(p.ExternalReference, LinkRef) && w.Charges != nil {
		if ch, err := w.Charges.MarkChargePaid(ctx, p.ExternalReference, p.ID); err == nil && ch != nil {
			// la regla "link de cobro de mano" lo sube a importante
			ev.Body += " Es el link de cobro de Mano a " + ch.Name + " por " + ch.Description + "."
			ev.Sender = ch.Name
		}
	}
	if err := w.Sink.OnEvent(ctx, ev); err != nil {
		slog.Error("mercadopago evento", "err", err)
	}
}

// Me devuelve el usuario de la cuenta: sirve para comprobar el access token.
func (c *Client) Me(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/users/me", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		Nickname string `json:"nickname"`
		Message  string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("mercadopago %d: %s", res.StatusCode, out.Message)
	}
	return out.Nickname, nil
}

// ── Cobros, devoluciones y movimientos ─────────────────────

func (c *Client) api(ctx context.Context, method, path, idemKey string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		req.Header.Set("X-Idempotency-Key", idemKey) // el mismo pedido dos veces no cobra ni devuelve dos veces
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Message
		if msg == "" {
			msg = core.Truncate(string(raw), 200)
		}
		return fmt.Errorf("mercadopago %d: %s", res.StatusCode, msg)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Link es un link de pago (preferencia de Checkout Pro).
type Link struct {
	ID, URL string
	Expires time.Time
}

// CreateLink arma un link de pago. ref vuelve en el pago (external_reference) para saber qué se cobró.
func (c *Client) CreateLink(ctx context.Context, title string, amount float64, ref, notifyURL string, ttl time.Duration) (Link, error) {
	exp := time.Now().Add(ttl)
	body := map[string]any{
		"items":              []map[string]any{{"title": core.Truncate(title, 250), "quantity": 1, "unit_price": amount, "currency_id": "ARS"}},
		"external_reference": ref,
		"expires":            true,
		"expiration_date_to": exp.Format("2006-01-02T15:04:05.000-07:00"),
	}
	if notifyURL != "" {
		body["notification_url"] = notifyURL
	}
	var out struct {
		ID        string `json:"id"`
		InitPoint string `json:"init_point"`
	}
	if err := c.api(ctx, http.MethodPost, "/checkout/preferences", "", body, &out); err != nil {
		return Link{}, err
	}
	if out.InitPoint == "" {
		return Link{}, fmt.Errorf("mercadopago no devolvió el link")
	}
	return Link{ID: out.ID, URL: out.InitPoint, Expires: exp}, nil
}

// Refund devuelve un pago (amount 0 = total). key evita devolver dos veces.
func (c *Client) Refund(ctx context.Context, paymentID string, amount float64, key string) (string, error) {
	var body any = map[string]any{}
	if amount > 0 {
		body = map[string]any{"amount": amount}
	}
	var out struct {
		ID     json.Number `json:"id"`
		Status string      `json:"status"`
	}
	if err := c.api(ctx, http.MethodPost, "/v1/payments/"+paymentID+"/refunds", key, body, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}

// Summary resume lo cobrado en un período.
type Summary struct {
	Count          int
	Gross, Net     float64
	Refunded       float64
	Top            []Payment
	PendingRelease float64 // cobrado pero todavía no liberado (money_release_date futura)
}

// Recent suma los pagos acreditados de los últimos days días (búsqueda oficial; no hay endpoint de saldo).
func (c *Client) Recent(ctx context.Context, days int) (Summary, error) {
	var s Summary
	for offset := 0; offset < 1000; offset += 100 {
		path := fmt.Sprintf("/v1/payments/search?sort=date_created&criteria=desc&range=date_created&begin_date=NOW-%dDAYS&end_date=NOW&status=approved&limit=100&offset=%d", days, offset)
		var out struct {
			Paging struct {
				Total int `json:"total"`
			} `json:"paging"`
			Results []struct {
				Payment
				Refunded float64 `json:"transaction_amount_refunded"`
				Release  string  `json:"money_release_date"`
				Details  struct {
					Net float64 `json:"net_received_amount"`
				} `json:"transaction_details"`
			} `json:"results"`
		}
		if err := c.api(ctx, http.MethodGet, path, "", nil, &out); err != nil {
			return s, err
		}
		for _, r := range out.Results {
			s.Count++
			s.Gross += r.TransactionAmount
			s.Net += r.Details.Net
			s.Refunded += r.Refunded
			if t, err := time.Parse(time.RFC3339, r.Release); err == nil && t.After(time.Now()) {
				s.PendingRelease += r.Details.Net
			}
			if len(s.Top) < 5 {
				s.Top = append(s.Top, r.Payment)
			}
		}
		if offset+100 >= out.Paging.Total || len(out.Results) == 0 {
			break
		}
	}
	return s, nil
}

// Money formatea pesos al estilo argentino: 150000 → "$150.000".
func Money(v float64) string { return "$" + money(v) }

// ParseAmount entiende "150000", "150.000", "$150.000,50" y "150k".
func ParseAmount(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	mult := 1.0
	if strings.HasSuffix(strings.ToLower(s), "k") {
		mult, s = 1000, s[:len(s)-1]
	}
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v * mult, true
}

// LinkRef es el prefijo de external_reference de los links que arma Mano.
const LinkRef = "mano-"
