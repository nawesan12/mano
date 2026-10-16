package mercadopago

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/nawesan12/mano/worker/internal/core"
)

// Payouts manda dinero desde la cuenta (API de Payouts). En producción cada pedido va firmado con Ed25519
// y la clave pública tiene que estar registrada por Mercado Pago: sin eso responde 403.
type Payouts struct {
	c    *Client
	key  ed25519.PrivateKey
	test bool // cuentas de prueba: sin firma obligatoria
}

// ParsePrivateKey acepta PEM PKCS#8 (openssl genpkey -algorithm ed25519) o la semilla de 32 bytes en base64.
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "-----BEGIN") {
		b, _ := pem.Decode([]byte(s))
		if b == nil {
			return nil, errors.New("PEM inválido")
		}
		k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
		if err != nil {
			return nil, err
		}
		pk, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("la clave no es Ed25519")
		}
		return pk, nil
	}
	seed, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("la clave tiene que ser PEM PKCS#8 o una semilla Ed25519 de 32 bytes en base64")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func NewPayouts(c *Client, key ed25519.PrivateKey, test bool) *Payouts {
	return &Payouts{c: c, key: key, test: test}
}

// Destination es a dónde va la plata: otra cuenta de Mercado Pago (email) o un CBU/CVU.
type Destination struct {
	Email  string `json:"email,omitempty"`
	CBU    string `json:"cbu,omitempty"`
	Holder string `json:"holder,omitempty"`
	DNI    string `json:"dni,omitempty"`
}

func (d Destination) String() string {
	if d.Email != "" {
		return d.Email
	}
	return d.Holder + " (CBU " + d.CBU[:4] + "…" + d.CBU[len(d.CBU)-4:] + ")"
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ValidCBU comprueba los dos dígitos verificadores del CBU/CVU (22 dígitos). Un dígito mal tipeado no pasa.
func ValidCBU(cbu string) bool {
	if len(cbu) != 22 {
		return false
	}
	d := make([]int, 22)
	for i, r := range cbu {
		if r < '0' || r > '9' {
			return false
		}
		d[i] = int(r - '0')
	}
	check := func(digits []int, weights []int) int {
		sum := 0
		for i, v := range digits {
			sum += v * weights[i%len(weights)]
		}
		return (10 - sum%10) % 10
	}
	// bloque 1: entidad (3) + sucursal (4), verificador en la posición 8
	if check(d[0:7], []int{7, 1, 3, 9, 7, 1, 3}) != d[7] {
		return false
	}
	// bloque 2: cuenta (13), verificador en la posición 22
	return check(d[8:21], []int{3, 9, 7, 1}) == d[21]
}

func (d Destination) Validate() error {
	switch {
	case d.Email != "":
		if !emailRe.MatchString(d.Email) {
			return errors.New("ese mail no es válido")
		}
	case d.CBU != "":
		if !ValidCBU(d.CBU) {
			return errors.New("ese CBU/CVU no es válido (revisá los 22 dígitos)")
		}
		if strings.TrimSpace(d.Holder) == "" || len(d.DNI) < 7 {
			return errors.New("para un CBU necesito el titular y su DNI")
		}
	default:
		return errors.New("falta el destino: un mail de Mercado Pago o un CBU/CVU")
	}
	return nil
}

func (d Destination) account() map[string]any {
	if d.Email != "" {
		return map[string]any{"email": d.Email}
	}
	return map[string]any{"holder": d.Holder, "number": d.CBU, "bank_id": d.CBU[0:3], "branch": d.CBU[3:7], "owner_value": d.DNI, "owner_type": "DNI"}
}

// PayoutResult es el estado de una transferencia.
type PayoutResult struct {
	ID, TransactionID, Status, Detail string
}

func (p *Payouts) do(ctx context.Context, method, path, idemKey string, body, out any) error {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.c.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.c.token)
	req.Header.Set("Content-Type", "application/json")
	if idemKey != "" {
		req.Header.Set("X-Idempotency-Key", idemKey)
	}
	if p.test {
		req.Header.Set("X-test-token", "true")
		req.Header.Set("X-enforce-signature", "false")
	} else if body != nil {
		req.Header.Set("X-signature", base64.StdEncoding.EncodeToString(ed25519.Sign(p.key, raw)))
		req.Header.Set("X-enforce-signature", "true")
	}
	res, err := p.c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == http.StatusForbidden {
		return errors.New("Mercado Pago no habilitó las transferencias para esta cuenta (403): pedí la habilitación y registrá la clave pública")
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("mercadopago payouts %d: %s", res.StatusCode, core.Truncate(string(b), 200))
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

type payoutBody struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Transactions []struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		StatusDetail string `json:"status_detail"`
	} `json:"transactions"`
}

func (b payoutBody) result() PayoutResult {
	r := PayoutResult{ID: b.ID, Status: b.Status}
	if len(b.Transactions) > 0 {
		t := b.Transactions[0]
		r.TransactionID, r.Detail = t.ID, t.StatusDetail
		if t.Status != "" {
			r.Status = t.Status
		}
	}
	return r
}

// Send crea una transferencia de una sola transacción. ref es único por aprobación (y su idempotencia).
func (p *Payouts) Send(ctx context.Context, ref, description string, d Destination, amount float64, notifyURL string) (PayoutResult, error) {
	if err := d.Validate(); err != nil {
		return PayoutResult{}, err
	}
	body := map[string]any{
		"external_reference": ref,
		"description":        core.Truncate(description, 100),
		"transactions": []map[string]any{{
			"type": "account", "account": d.account(), "external_reference": ref + "-1",
			"amount": map[string]any{"currency": "ARS", "value": amount},
		}},
	}
	if notifyURL != "" {
		body["config"] = map[string]string{"notification_url": notifyURL}
	}
	var out payoutBody
	if err := p.do(ctx, http.MethodPost, "/v1/payouts", ref, body, &out); err != nil {
		return PayoutResult{}, err
	}
	return out.result(), nil
}

// Status consulta una transferencia (el resultado es asíncrono). Lee el resumen del lote: con una sola
// transacción, el lote dice si salió, falló o se canceló.
func (p *Payouts) Status(ctx context.Context, payoutID string) (PayoutResult, error) {
	var out struct {
		Status  string `json:"status"`
		Summary map[string]struct {
			Items int `json:"items"`
		} `json:"summary"`
	}
	if err := p.do(ctx, http.MethodGet, "/v1/payouts/"+payoutID, "", nil, &out); err != nil {
		return PayoutResult{}, err
	}
	r := PayoutResult{ID: payoutID, Status: "in_process"}
	switch {
	case out.Summary["success"].Items > 0:
		r.Status = "success"
	case out.Summary["error"].Items > 0:
		r.Status = "error"
	case out.Summary["canceled"].Items > 0:
		r.Status = "canceled"
	}
	return r, nil
}

// PayoutFinal dice si el estado ya no cambia, y si salió bien.
func PayoutFinal(status string) (final, ok bool) {
	switch {
	case strings.HasPrefix(status, "success"), status == "accredited", status == "processed":
		return true, true
	case strings.HasPrefix(status, "rejected"), strings.HasPrefix(status, "error"), status == "canceled", status == "refunded":
		return true, false
	}
	return false, false
}
