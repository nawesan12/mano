// Package mercadolibre sigue las ventas y los envíos de Mercado Libre (Mercado Envíos).
// La API de Mercado Pago no expone envíos en Argentina: hace falta el OAuth de Mercado Libre del vendedor.
package mercadolibre

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
)

// TokenStore guarda el refresh token: en Mercado Libre es de un solo uso y cada renovación trae uno nuevo.
type TokenStore interface {
	MLRefreshToken(ctx context.Context) (string, error)
	SetMLRefreshToken(ctx context.Context, token string, userID int64) error
}

type Client struct {
	clientID, secret, initialRefresh string
	tokens                           TokenStore
	base                             string
	hc                               *http.Client

	mu      sync.Mutex
	access  string
	expires time.Time
	userID  int64
}

func NewClient(clientID, secret, refresh string, tokens TokenStore) *Client {
	return &Client{clientID: clientID, secret: secret, initialRefresh: refresh, tokens: tokens,
		base: "https://api.mercadolibre.com", hc: &http.Client{Timeout: 20 * time.Second}}
}

// token renueva el acceso (dura 6 h) y guarda el refresh token nuevo antes de usarlo.
func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.access != "" && time.Now().Before(c.expires) {
		return c.access, nil
	}
	refresh, err := c.tokens.MLRefreshToken(ctx)
	if err != nil {
		return "", err
	}
	if refresh == "" {
		refresh = c.initialRefresh
	}
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {c.clientID}, "client_secret": {c.secret}, "refresh_token": {refresh}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		UserID       int64  `json:"user_id"`
		Message      string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	if res.StatusCode >= 300 || out.AccessToken == "" {
		return "", fmt.Errorf("no pude renovar el acceso a Mercado Libre (%d %s): volvé a autorizar la app", res.StatusCode, out.Message)
	}
	// el refresh token viejo ya no sirve: si no se guarda el nuevo, se pierde el acceso
	if out.RefreshToken != "" {
		if err := c.tokens.SetMLRefreshToken(ctx, out.RefreshToken, out.UserID); err != nil {
			slog.Error("mercadolibre: no pude guardar el refresh token nuevo", "err", err)
		}
	}
	c.access, c.userID = out.AccessToken, out.UserID
	c.expires = time.Now().Add(time.Duration(out.ExpiresIn-120) * time.Second)
	return c.access, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("x-format-new", "true")
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("mercadolibre %s: %d %s", path, res.StatusCode, core.Truncate(string(raw), 200))
	}
	return json.Unmarshal(raw, out)
}

// Me devuelve el usuario del vendedor (para el chequeo y para validar notificaciones).
func (c *Client) Me(ctx context.Context) (int64, string, error) {
	var out struct {
		ID       int64  `json:"id"`
		Nickname string `json:"nickname"`
	}
	err := c.get(ctx, "/users/me", &out)
	return out.ID, out.Nickname, err
}

type Shipment struct {
	ID          int64  `json:"id"`
	Status      string `json:"status"`    // pending | handling | ready_to_ship | shipped | delivered | not_delivered | cancelled
	Substatus   string `json:"substatus"` // printed, waiting_for_withdrawal, returning_to_sender…
	Tracking    string `json:"tracking_number"`
	OrderID     int64  `json:"order_id"`
	Destination struct {
		ReceiverName string `json:"receiver_name"`
		Address      struct {
			City struct {
				Name string `json:"name"`
			} `json:"city"`
		} `json:"shipping_address"`
	} `json:"destination"`
}

func (c *Client) Shipment(ctx context.Context, id string) (Shipment, error) {
	var s Shipment
	return s, c.get(ctx, "/shipments/"+id, &s)
}

type Order struct {
	ID          int64   `json:"id"`
	Status      string  `json:"status"` // paid | cancelled | …
	TotalAmount float64 `json:"total_amount"`
	Buyer       struct {
		Nickname string `json:"nickname"`
	} `json:"buyer"`
	Items []struct {
		Item struct {
			Title string `json:"title"`
		} `json:"item"`
		Quantity int `json:"quantity"`
	} `json:"order_items"`
	Shipping struct {
		ID int64 `json:"id"`
	} `json:"shipping"`
}

func (c *Client) Order(ctx context.Context, id string) (Order, error) {
	var o Order
	return o, c.get(ctx, "/orders/"+id, &o)
}

var shipLabel = map[string]string{
	"ready_to_ship": "listo para despachar", "shipped": "en camino", "delivered": "entregado",
	"not_delivered": "no se pudo entregar", "cancelled": "cancelado",
}

// ShipmentEvent normaliza un envío. Estados intermedios (pending, handling) no generan eventos.
func ShipmentEvent(s Shipment, tenantID string) (core.Event, bool) {
	label, ok := shipLabel[s.Status]
	if !ok {
		return core.Event{}, false
	}
	who := s.Destination.ReceiverName
	body := fmt.Sprintf("Envío %d %s", s.ID, label)
	if who != "" {
		body += " (para " + who
		if city := s.Destination.Address.City.Name; city != "" {
			body += ", " + city
		}
		body += ")"
	}
	if s.Substatus != "" {
		body += ". Detalle: " + strings.ReplaceAll(s.Substatus, "_", " ")
	}
	if s.Status == "ready_to_ship" {
		body += ". Imprimí la etiqueta y despachalo."
	}
	return core.Event{TenantID: tenantID, Source: "mercadolibre", Kind: "shipment." + s.Status,
		ExternalID: strconv.FormatInt(s.ID, 10) + ":" + s.Status + ":" + s.Substatus, Sender: who,
		Subject: "Envío " + label, Body: body + ".", ReceivedAt: time.Now(),
		Payload: map[string]any{"shipmentId": s.ID, "orderId": s.OrderID, "tracking": s.Tracking}}, true
}

// OrderEvent normaliza una venta pagada o cancelada.
func OrderEvent(o Order, tenantID string) (core.Event, bool) {
	if o.Status != "paid" && o.Status != "cancelled" {
		return core.Event{}, false
	}
	var items []string
	for _, it := range o.Items {
		items = append(items, fmt.Sprintf("%dx %s", it.Quantity, it.Item.Title))
	}
	verb := "Venta nueva"
	if o.Status == "cancelled" {
		verb = "Venta cancelada"
	}
	amount := o.TotalAmount
	return core.Event{TenantID: tenantID, Source: "mercadolibre", Kind: "order." + o.Status,
		ExternalID: strconv.FormatInt(o.ID, 10) + ":" + o.Status, Sender: o.Buyer.Nickname, Subject: verb,
		Body: fmt.Sprintf("%s en Mercado Libre: %s, $%.0f.", verb, strings.Join(items, ", "), o.TotalAmount), Amount: &amount, ReceivedAt: time.Now(),
		Payload: map[string]any{"orderId": o.ID, "shipmentId": o.Shipping.ID}}, true
}

// ── Notificaciones ─────────────────────────────────────────

type EventSink interface {
	OnEvent(ctx context.Context, ev core.Event) error
}

// ShipmentLog guarda el último estado de cada envío para /envios.
type ShipmentLog interface {
	UpsertShipment(ctx context.Context, id int64, status, substatus, receiver, tracking string) error
}

// Webhook recibe las notificaciones de Mercado Libre. No vienen firmadas: se valida que sean de nuestra app
// y de nuestro vendedor, y el recurso siempre se vuelve a consultar a la API (nunca se confía en el aviso).
type Webhook struct {
	Client   *Client
	AppID    string
	UserID   int64 // vendedor; 0 = se toma del primer token
	Sink     EventSink
	Log      ShipmentLog
	TenantID string
	Base     context.Context
}

func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var n struct {
		Resource      string `json:"resource"`
		UserID        int64  `json:"user_id"`
		Topic         string `json:"topic"`
		ApplicationID int64  `json:"application_id"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&n)
	rw.WriteHeader(http.StatusOK) // Mercado Libre espera respuesta enseguida
	if strconv.FormatInt(n.ApplicationID, 10) != w.AppID {
		return
	}
	if w.UserID != 0 && n.UserID != w.UserID {
		return
	}
	id := n.Resource[strings.LastIndex(n.Resource, "/")+1:]
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return
	}
	switch n.Topic {
	case "shipments", "orders_v2":
		go w.process(n.Topic, id)
	}
}

func (w *Webhook) process(topic, id string) {
	ctx, cancel := context.WithTimeout(w.Base, 2*time.Minute)
	defer cancel()
	var ev core.Event
	var ok bool
	switch topic {
	case "shipments":
		s, err := w.Client.Shipment(ctx, id)
		if err != nil {
			slog.Error("mercadolibre envío", "err", err)
			return
		}
		if w.Log != nil {
			_ = w.Log.UpsertShipment(ctx, s.ID, s.Status, s.Substatus, s.Destination.ReceiverName, s.Tracking)
		}
		ev, ok = ShipmentEvent(s, w.TenantID)
	case "orders_v2":
		o, err := w.Client.Order(ctx, id)
		if err != nil {
			slog.Error("mercadolibre venta", "err", err)
			return
		}
		ev, ok = OrderEvent(o, w.TenantID)
	}
	if !ok {
		return
	}
	if err := w.Sink.OnEvent(ctx, ev); err != nil {
		slog.Error("mercadolibre evento", "err", err)
	}
}
