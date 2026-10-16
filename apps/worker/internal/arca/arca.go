package arca

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
)

// Endpoints de homologación (pruebas) y producción.
var endpoints = map[string][2]string{
	"homologacion": {"https://wsaahomo.afip.gov.ar/ws/services/LoginCms", "https://wswhomo.afip.gov.ar/wsfev1/service.asmx"},
	"produccion":   {"https://wsaa.afip.gov.ar/ws/services/LoginCms", "https://servicios1.afip.gov.ar/wsfev1/service.asmx"},
}

// Ticket es el acceso que da WSAA: dura ~12 h y no se puede pedir otro mientras siga vigente.
type Ticket struct {
	Token, Sign string
	Expires     time.Time
}

// TicketStore guarda el ticket: si el worker se reinicia, WSAA rechaza uno nuevo ("ya posee un TA válido").
type TicketStore interface {
	ArcaTicket(ctx context.Context) (Ticket, error)
	SetArcaTicket(ctx context.Context, t Ticket) error
}

type Client struct {
	CUIT     int64
	PtoVta   int
	wsaa     string
	wsfe     string
	cert     *x509.Certificate
	key      *rsa.PrivateKey
	tickets  TicketStore
	hc       *http.Client
	mu       sync.Mutex
	cached   Ticket
	location *time.Location
}

// LoadKeyPair lee el certificado (PEM) que da ARCA y la clave privada (PEM, PKCS#1 o PKCS#8, RSA).
func LoadKeyPair(certPEM, keyPEM []byte) (*x509.Certificate, *rsa.PrivateKey, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, nil, errors.New("el certificado no es PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, nil, errors.New("la clave no es PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(kb.Bytes); err == nil {
		return cert, k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("la clave tiene que ser RSA")
	}
	return cert, rk, nil
}

func NewClient(env string, cuit int64, ptoVta int, cert *x509.Certificate, key *rsa.PrivateKey, tickets TicketStore, loc *time.Location) (*Client, error) {
	ep, ok := endpoints[env]
	if !ok {
		return nil, fmt.Errorf("ARCA_ENV tiene que ser homologacion o produccion")
	}
	return &Client{CUIT: cuit, PtoVta: ptoVta, wsaa: ep[0], wsfe: ep[1], cert: cert, key: key, tickets: tickets,
		hc: &http.Client{Timeout: 40 * time.Second}, location: loc}, nil
}

func (c *Client) soap(ctx context.Context, url, action, body string, out any) error {
	env := `<?xml version="1.0" encoding="UTF-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body>` + body + `</soapenv:Body></soapenv:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(env))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", action)
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	var fault struct {
		Body struct {
			Fault *struct {
				String string `xml:"faultstring"`
			} `xml:"Fault"`
		} `xml:"Body"`
	}
	if xml.Unmarshal(raw, &fault) == nil && fault.Body.Fault != nil {
		return fmt.Errorf("ARCA: %s", core.Truncate(fault.Body.Fault.String, 300))
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("ARCA %d: %s", res.StatusCode, core.Truncate(string(raw), 300))
	}
	return xml.Unmarshal(raw, out)
}

// ── WSAA ───────────────────────────────────────────────────

const arTime = "2006-01-02T15:04:05-07:00"

// ticket devuelve un acceso vigente: el de memoria, el guardado, o uno nuevo firmando el TRA con el certificado.
func (c *Client) ticket(ctx context.Context) (Ticket, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fresh := func(t Ticket) bool { return t.Token != "" && time.Until(t.Expires) > 5*time.Minute }
	if fresh(c.cached) {
		return c.cached, nil
	}
	if t, err := c.tickets.ArcaTicket(ctx); err == nil && fresh(t) {
		c.cached = t
		return t, nil
	}
	now := time.Now().In(c.location)
	tra := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><loginTicketRequest version="1.0"><header><uniqueId>%d</uniqueId><generationTime>%s</generationTime><expirationTime>%s</expirationTime></header><service>wsfe</service></loginTicketRequest>`,
		now.Unix(), now.Add(-10*time.Minute).Format(arTime), now.Add(10*time.Minute).Format(arTime))
	cms, err := SignCMS([]byte(tra), c.cert, c.key, now)
	if err != nil {
		return Ticket{}, err
	}
	var res struct {
		Body struct {
			Resp struct {
				Return string `xml:"loginCmsReturn"`
			} `xml:"loginCmsResponse"`
		} `xml:"Body"`
	}
	body := `<wsaa:loginCms xmlns:wsaa="http://wsaa.view.sua.dvadac.desein.afip.gov"><wsaa:in0>` + base64.StdEncoding.EncodeToString(cms) + `</wsaa:in0></wsaa:loginCms>`
	if err := c.soap(ctx, c.wsaa, "", body, &res); err != nil {
		return Ticket{}, err
	}
	var ta struct {
		Header struct {
			Expiration string `xml:"expirationTime"`
		} `xml:"header"`
		Credentials struct {
			Token string `xml:"token"`
			Sign  string `xml:"sign"`
		} `xml:"credentials"`
	}
	if err := xml.Unmarshal([]byte(html.UnescapeString(res.Body.Resp.Return)), &ta); err != nil || ta.Credentials.Token == "" {
		return Ticket{}, errors.New("WSAA no devolvió el ticket de acceso")
	}
	exp, err := time.Parse("2006-01-02T15:04:05.999-07:00", ta.Header.Expiration)
	if err != nil {
		exp = now.Add(11 * time.Hour)
	}
	t := Ticket{Token: ta.Credentials.Token, Sign: ta.Credentials.Sign, Expires: exp}
	_ = c.tickets.SetArcaTicket(ctx, t)
	c.cached = t
	return t, nil
}

// ── WSFEv1 ─────────────────────────────────────────────────

const fev1 = "http://ar.gov.afip.dif.FEV1/"

func (c *Client) auth(t Ticket) string {
	return fmt.Sprintf(`<ar:Auth><ar:Token>%s</ar:Token><ar:Sign>%s</ar:Sign><ar:Cuit>%d</ar:Cuit></ar:Auth>`, xmlEsc(t.Token), xmlEsc(t.Sign), c.CUIT)
}

func xmlEsc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

type wsErr struct {
	Code int    `xml:"Code"`
	Msg  string `xml:"Msg"`
}

func errText(errs []wsErr) string {
	var parts []string
	for _, e := range errs {
		parts = append(parts, fmt.Sprintf("%d %s", e.Code, e.Msg))
	}
	return strings.Join(parts, "; ")
}

// LastNumber es el último comprobante autorizado de ese tipo en el punto de venta.
func (c *Client) LastNumber(ctx context.Context, cbteTipo int) (int, error) {
	t, err := c.ticket(ctx)
	if err != nil {
		return 0, err
	}
	var res struct {
		Body struct {
			R struct {
				Result struct {
					CbteNro int     `xml:"CbteNro"`
					Errors  []wsErr `xml:"Errors>Err"`
				} `xml:"FECompUltimoAutorizadoResult"`
			} `xml:"FECompUltimoAutorizadoResponse"`
		} `xml:"Body"`
	}
	body := fmt.Sprintf(`<ar:FECompUltimoAutorizado xmlns:ar="%s">%s<ar:PtoVta>%d</ar:PtoVta><ar:CbteTipo>%d</ar:CbteTipo></ar:FECompUltimoAutorizado>`, fev1, c.auth(t), c.PtoVta, cbteTipo)
	if err := c.soap(ctx, c.wsfe, fev1+"FECompUltimoAutorizado", body, &res); err != nil {
		return 0, err
	}
	if e := res.Body.R.Result.Errors; len(e) > 0 {
		return 0, fmt.Errorf("ARCA: %s", errText(e))
	}
	return res.Body.R.Result.CbteNro, nil
}

// Invoice es lo que se factura. Fase 0: Factura C a consumidor final (monotributo).
type Invoice struct {
	CbteTipo int     // 11 = Factura C
	Concepto int     // 1 productos, 2 servicios, 3 ambos
	Total    float64 // en pesos
	DocTipo  int     // 99 = sin identificar (consumidor final); 96 = DNI; 80 = CUIT
	DocNro   int64
	Date     time.Time
}

// Result es el comprobante autorizado.
type Result struct {
	Number  int
	CAE     string
	Expires string // AAAAMMDD
	Obs     string
}

func money2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// Authorize pide el CAE para el próximo número. ARCA rechaza números salteados: se pide el último antes.
func (c *Client) Authorize(ctx context.Context, inv Invoice) (Result, error) {
	if inv.Total <= 0 {
		return Result{}, errors.New("el total tiene que ser mayor a cero")
	}
	last, err := c.LastNumber(ctx, inv.CbteTipo)
	if err != nil {
		return Result{}, err
	}
	t, err := c.ticket(ctx)
	if err != nil {
		return Result{}, err
	}
	n := last + 1
	day := inv.Date.In(c.location).Format("20060102")
	serv := ""
	if inv.Concepto == 2 || inv.Concepto == 3 {
		serv = fmt.Sprintf(`<ar:FchServDesde>%s</ar:FchServDesde><ar:FchServHasta>%s</ar:FchServHasta><ar:FchVtoPago>%s</ar:FchVtoPago>`, day, day, day)
	}
	condIVA := 5 // consumidor final (obligatorio desde RG 5616)
	body := fmt.Sprintf(`<ar:FECAESolicitar xmlns:ar="%s">%s<ar:FeCAEReq><ar:FeCabReq><ar:CantReg>1</ar:CantReg><ar:PtoVta>%d</ar:PtoVta><ar:CbteTipo>%d</ar:CbteTipo></ar:FeCabReq>`+
		`<ar:FeDetReq><ar:FECAEDetRequest><ar:Concepto>%d</ar:Concepto><ar:DocTipo>%d</ar:DocTipo><ar:DocNro>%d</ar:DocNro><ar:CbteDesde>%d</ar:CbteDesde><ar:CbteHasta>%d</ar:CbteHasta>`+
		`<ar:CbteFch>%s</ar:CbteFch><ar:ImpTotal>%s</ar:ImpTotal><ar:ImpTotConc>0</ar:ImpTotConc><ar:ImpNeto>%s</ar:ImpNeto><ar:ImpOpEx>0</ar:ImpOpEx><ar:ImpTrib>0</ar:ImpTrib><ar:ImpIVA>0</ar:ImpIVA>`+
		`%s<ar:MonId>PES</ar:MonId><ar:MonCotiz>1</ar:MonCotiz><ar:CondicionIVAReceptorId>%d</ar:CondicionIVAReceptorId></ar:FECAEDetRequest></ar:FeDetReq></ar:FeCAEReq></ar:FECAESolicitar>`,
		fev1, c.auth(t), c.PtoVta, inv.CbteTipo, inv.Concepto, inv.DocTipo, inv.DocNro, n, n, day, money2(inv.Total), money2(inv.Total), serv, condIVA)
	var res struct {
		Body struct {
			R struct {
				Result struct {
					Det struct {
						Resultado string  `xml:"Resultado"`
						CAE       string  `xml:"CAE"`
						CAEFchVto string  `xml:"CAEFchVto"`
						Obs       []wsErr `xml:"Observaciones>Obs"`
					} `xml:"FeDetResp>FECAEDetResponse"`
					Errors []wsErr `xml:"Errors>Err"`
				} `xml:"FECAESolicitarResult"`
			} `xml:"FECAESolicitarResponse"`
		} `xml:"Body"`
	}
	if err := c.soap(ctx, c.wsfe, fev1+"FECAESolicitar", body, &res); err != nil {
		return Result{}, err
	}
	r := res.Body.R.Result
	if len(r.Errors) > 0 {
		return Result{}, fmt.Errorf("ARCA rechazó la factura: %s", errText(r.Errors))
	}
	if r.Det.Resultado != "A" || r.Det.CAE == "" {
		return Result{}, fmt.Errorf("ARCA rechazó la factura: %s", errText(r.Det.Obs))
	}
	return Result{Number: n, CAE: r.Det.CAE, Expires: r.Det.CAEFchVto, Obs: errText(r.Det.Obs)}, nil
}

// QRURL es el link del QR obligatorio del comprobante (RG 4892): sirve para verificarlo en ARCA.
func (c *Client) QRURL(inv Invoice, r Result) string {
	data, _ := json.Marshal(map[string]any{
		"ver": 1, "fecha": inv.Date.In(c.location).Format("2006-01-02"), "cuit": c.CUIT, "ptoVta": c.PtoVta, "tipoCmp": inv.CbteTipo,
		"nroCmp": r.Number, "importe": inv.Total, "moneda": "PES", "ctz": 1, "tipoDocRec": inv.DocTipo, "nroDocRec": inv.DocNro,
		"tipoCodAut": "E", "codAut": json.Number(r.CAE),
	})
	return "https://www.afip.gob.ar/fe/qr/?p=" + base64.StdEncoding.EncodeToString(data)
}

// Label: "Factura C 0003-00000124".
func (c *Client) Label(cbteTipo, number int) string {
	kind := map[int]string{11: "Factura C", 6: "Factura B", 1: "Factura A"}[cbteTipo]
	if kind == "" {
		kind = "Comprobante " + strconv.Itoa(cbteTipo)
	}
	return fmt.Sprintf("%s %04d-%08d", kind, c.PtoVta, number)
}
