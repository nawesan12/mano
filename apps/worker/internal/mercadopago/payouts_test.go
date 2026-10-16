package mercadopago

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cbu arma un CBU válido a partir de banco+sucursal (7) y cuenta (13), calculando los verificadores.
func cbu(block1, block2 string) string {
	sum := func(s string, w []int) int {
		t := 0
		for i, r := range s {
			t += int(r-'0') * w[i%len(w)]
		}
		return (10 - t%10) % 10
	}
	return block1 + fmt.Sprint(sum(block1, []int{7, 1, 3, 9, 7, 1, 3})) + block2 + fmt.Sprint(sum(block2, []int{3, 9, 7, 1}))
}

func TestValidCBUAndDestination(t *testing.T) {
	good := cbu("0170099", "2200000678901")
	if !ValidCBU(good) {
		t.Fatal(good)
	}
	bad := good[:10] + string('0'+(good[10]-'0'+1)%10) + good[11:]
	if ValidCBU(bad) || ValidCBU("123") {
		t.Fatal("un dígito mal tipeado no pasa")
	}
	if err := (Destination{CBU: good}).Validate(); err == nil {
		t.Fatal("CBU sin titular ni DNI")
	}
	if err := (Destination{CBU: good, Holder: "Juan Pérez", DNI: "12345678"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Destination{Email: "no-es-mail"}).Validate(); err == nil {
		t.Fatal("mail inválido")
	}
	acc := Destination{CBU: good, Holder: "Juan", DNI: "12345678"}.account()
	if acc["bank_id"] != "017" || acc["branch"] != "0099" {
		t.Fatal(acc)
	}
}

func TestParsePrivateKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if k, err := ParsePrivateKey(pemKey); err != nil || !k.Equal(priv) {
		t.Fatal("PEM", err)
	}
	if k, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(priv.Seed())); err != nil || !k.Equal(priv) {
		t.Fatal("semilla", err)
	}
	if _, err := ParsePrivateKey("basura"); err == nil {
		t.Fatal("basura")
	}
}

// Cada pedido va firmado con Ed25519 sobre el cuerpo exacto y con idempotencia; el estado sale del resumen del lote.
func TestPayoutSendSignedAndStatus(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/payouts":
			raw, _ := io.ReadAll(r.Body)
			sig, _ := base64.StdEncoding.DecodeString(r.Header.Get("X-signature"))
			if !ed25519.Verify(pub, raw, sig) || r.Header.Get("X-Idempotency-Key") != "mano-payout-ap1" || r.Header.Get("X-enforce-signature") != "true" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.Unmarshal(raw, &gotBody)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"POP1","status":"created","transactions":[{"id":"TOP1"}]}`))
		case r.URL.Path == "/v1/payouts/POP1":
			_, _ = w.Write([]byte(`{"status":"processed","summary":{"success":{"items":1},"error":{"items":0}}}`))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	c := NewClient("tok")
	c.base = srv.URL
	p := NewPayouts(c, priv, false)
	res, err := p.Send(context.Background(), "mano-payout-ap1", "Factura 88", Destination{Email: "prov@mail.com"}, 50000, "")
	if err != nil || res.ID != "POP1" || res.TransactionID != "TOP1" {
		t.Fatal(res, err)
	}
	tx := gotBody["transactions"].([]any)[0].(map[string]any)
	if tx["account"].(map[string]any)["email"] != "prov@mail.com" || tx["amount"].(map[string]any)["value"].(float64) != 50000 {
		t.Fatal(gotBody)
	}
	st, err := p.Status(context.Background(), "POP1")
	if final, ok := PayoutFinal(st.Status); err != nil || !final || !ok {
		t.Fatal(st, err)
	}
	if _, err := p.Status(context.Background(), "OTRO"); err == nil || !strings.Contains(err.Error(), "no habilitó") {
		t.Fatal("un 403 explica que falta la habilitación", err)
	}
}
