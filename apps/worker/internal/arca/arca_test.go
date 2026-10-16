package arca

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type memTickets struct {
	mu sync.Mutex
	t  Ticket
}

func (m *memTickets) ArcaTicket(context.Context) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t, nil
}
func (m *memTickets) SetArcaTicket(_ context.Context, t Ticket) error {
	m.mu.Lock()
	m.t = t
	m.mu.Unlock()
	return nil
}

// fakeARCA hace de WSAA y WSFEv1: cuenta los logins y numera los comprobantes.
type fakeARCA struct {
	mu     sync.Mutex
	logins int
	last   int
	bodies []string
	reject bool
}

var desdeRe = regexp.MustCompile(`<ar:CbteDesde>(\d+)</ar:CbteDesde>`)

func (f *fakeARCA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	body := string(raw)
	f.bodies = append(f.bodies, body)
	wrap := func(inner string) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` + inner + `</soap:Body></soap:Envelope>`))
	}
	switch {
	case r.URL.Path == "/wsaa":
		m := regexp.MustCompile(`<wsaa:in0>([^<]+)</wsaa:in0>`).FindStringSubmatch(body)
		if m == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := base64.StdEncoding.DecodeString(m[1]); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.logins++
		ta := `<?xml version="1.0" encoding="UTF-8"?><loginTicketResponse version="1.0"><header><expirationTime>` +
			time.Now().Add(12*time.Hour).Format("2006-01-02T15:04:05.000-07:00") + `</expirationTime></header><credentials><token>TOK</token><sign>SIG</sign></credentials></loginTicketResponse>`
		wrap(`<loginCmsResponse><loginCmsReturn>` + html.EscapeString(ta) + `</loginCmsReturn></loginCmsResponse>`)
	case r.Header.Get("SOAPAction") == fev1+"FECompUltimoAutorizado":
		wrap(`<FECompUltimoAutorizadoResponse xmlns="` + fev1 + `"><FECompUltimoAutorizadoResult><PtoVta>3</PtoVta><CbteTipo>11</CbteTipo><CbteNro>` + itoa(f.last) + `</CbteNro></FECompUltimoAutorizadoResult></FECompUltimoAutorizadoResponse>`)
	case r.Header.Get("SOAPAction") == fev1+"FECAESolicitar":
		if f.reject {
			wrap(`<FECAESolicitarResponse xmlns="` + fev1 + `"><FECAESolicitarResult><FeDetResp><FECAEDetResponse><Resultado>R</Resultado><Observaciones><Obs><Code>10015</Code><Msg>Falta el documento del receptor</Msg></Obs></Observaciones></FECAEDetResponse></FeDetResp></FECAESolicitarResult></FECAESolicitarResponse>`)
			return
		}
		n := desdeRe.FindStringSubmatch(body)
		if n == nil || n[1] != itoa(f.last+1) || !strings.Contains(body, "<ar:Token>TOK</ar:Token>") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.last++
		wrap(`<FECAESolicitarResponse xmlns="` + fev1 + `"><FECAESolicitarResult><FeDetResp><FECAEDetResponse><Resultado>A</Resultado><CAE>74123456789012</CAE><CAEFchVto>20261025</CAEFchVto></FECAEDetResponse></FeDetResp></FECAESolicitarResult></FECAESolicitarResponse>`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestAuthorizeInvoices(t *testing.T) {
	f := &fakeARCA{last: 123}
	srv := httptest.NewServer(f)
	defer srv.Close()
	cert, key := testCert(t)
	ar, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	tickets := &memTickets{}
	c, err := NewClient("homologacion", 20111111112, 3, cert, key, tickets, ar)
	if err != nil {
		t.Fatal(err)
	}
	c.wsaa, c.wsfe = srv.URL+"/wsaa", srv.URL+"/wsfe"
	inv := Invoice{CbteTipo: 11, Concepto: 2, Total: 150000, DocTipo: 99, Date: time.Date(2026, 10, 15, 12, 0, 0, 0, ar)}
	r, err := c.Authorize(context.Background(), inv)
	if err != nil || r.Number != 124 || r.CAE != "74123456789012" || c.Label(11, r.Number) != "Factura C 0003-00000124" {
		t.Fatal(r, err)
	}
	if _, err := c.Authorize(context.Background(), inv); err != nil || f.last != 125 {
		t.Fatal("la segunda factura toma el número siguiente:", err)
	}
	if f.logins != 1 {
		t.Fatalf("el ticket se reutiliza: %d logins", f.logins)
	}
	// un worker nuevo usa el ticket guardado (WSAA no da otro mientras siga vigente)
	c2, _ := NewClient("homologacion", 20111111112, 3, cert, key, tickets, ar)
	c2.wsaa, c2.wsfe = c.wsaa, c.wsfe
	if _, err := c2.LastNumber(context.Background(), 11); err != nil || f.logins != 1 {
		t.Fatal("después de un reinicio no vuelve a loguearse", err, f.logins)
	}
	var body string
	for _, b := range f.bodies {
		if strings.Contains(b, "FECAESolicitar") {
			body = b
		}
	}
	for _, want := range []string{"<ar:ImpTotal>150000.00</ar:ImpTotal>", "<ar:FchServDesde>20261015</ar:FchServDesde>", "<ar:CondicionIVAReceptorId>5</ar:CondicionIVAReceptorId>", "<ar:Cuit>20111111112</ar:Cuit>"} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %s en el pedido", want)
		}
	}
	nc := Invoice{CbteTipo: 13, Concepto: 2, Total: 50000, DocTipo: 99, Date: inv.Date, Assoc: &Assoc{Tipo: 11, PtoVta: 3, Nro: 124, Date: inv.Date}}
	if _, err := c.Authorize(context.Background(), nc); err != nil {
		t.Fatal(err)
	}
	last := f.bodies[len(f.bodies)-1]
	if !strings.Contains(last, "<ar:CbteTipo>13</ar:CbteTipo>") || !strings.Contains(last, "<ar:CbtesAsoc><ar:CbteAsoc><ar:Tipo>11</ar:Tipo><ar:PtoVta>3</ar:PtoVta><ar:Nro>124</ar:Nro>") {
		t.Fatal("la nota de crédito lleva el comprobante asociado:", last)
	}
	if c.Label(13, 1) != "Nota de Crédito C 0003-00000001" {
		t.Fatal(c.Label(13, 1))
	}
	f.reject = true
	if _, err := c.Authorize(context.Background(), inv); err == nil || !strings.Contains(err.Error(), "Falta el documento") {
		t.Fatal("un rechazo explica el motivo:", err)
	}
	if u := c.QRURL(inv, r); !strings.HasPrefix(u, "https://www.afip.gob.ar/fe/qr/?p=") {
		t.Fatal(u)
	}
}
