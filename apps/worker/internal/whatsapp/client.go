// Package whatsapp integra WhatsApp Cloud API: envío (texto, botones, plantillas),
// webhook firmado e idempotente, ventana de 24 h y costo real por mensaje.
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"regexp"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

// Window es la ventana de servicio: fuera de ella solo se puede escribir con plantilla aprobada.
const Window = 24 * time.Hour

var ErrWindowClosed = errors.New("pasaron más de 24 h desde su último mensaje: WhatsApp solo deja escribirle con una plantilla aprobada")

type Client struct {
	phoneID, token, base string
	hc                   *http.Client
	store                *store.Store
	now                  func() time.Time
}

func NewClient(phoneID, token, apiVersion string, s *store.Store) *Client {
	return &Client{phoneID: phoneID, token: token, base: "https://graph.facebook.com/" + apiVersion,
		hc: &http.Client{Timeout: 30 * time.Second}, store: s, now: time.Now}
}

func (c *Client) do(ctx context.Context, method, url string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, url, rd)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("whatsapp %d (código %d): %s", res.StatusCode, e.Error.Code, core.Truncate(e.Error.Message, 200))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// send manda un mensaje y lo registra para cruzarlo después con el costo que informa Meta.
func (c *Client) send(ctx context.Context, to, kind, template string, payload map[string]any) (string, error) {
	payload["messaging_product"] = "whatsapp"
	payload["recipient_type"] = "individual"
	payload["to"] = to
	var out struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := c.do(ctx, http.MethodPost, c.base+"/"+c.phoneID+"/messages", payload, &out); err != nil {
		return "", err
	}
	if len(out.Messages) == 0 {
		return "", errors.New("whatsapp no devolvió id de mensaje")
	}
	wamid := out.Messages[0].ID
	if c.store != nil {
		_ = c.store.RecordOutbound(ctx, wamid, to, kind, template)
	}
	return wamid, nil
}

func (c *Client) SendText(ctx context.Context, to, body string) error {
	_, err := c.send(ctx, to, "text", "", map[string]any{"type": "text", "text": map[string]any{"body": core.Truncate(body, 4096), "preview_url": false}})
	return err
}

type Button struct{ ID, Title string }

// SendButtons: hasta 3 botones, título de ~20 caracteres, cuerpo de ~1024.
func (c *Client) SendButtons(ctx context.Context, to, body string, buttons []Button) error {
	if len(buttons) > 3 {
		buttons = buttons[:3]
	}
	bs := make([]map[string]any, len(buttons))
	for i, b := range buttons {
		bs[i] = map[string]any{"type": "reply", "reply": map[string]string{"id": b.ID, "title": core.Truncate(b.Title, 20)}}
	}
	_, err := c.send(ctx, to, "interactive", "", map[string]any{"type": "interactive", "interactive": map[string]any{
		"type": "button", "body": map[string]string{"text": core.Truncate(body, 1024)}, "action": map[string]any{"buttons": bs},
	}})
	return err
}

var paramSpaces = regexp.MustCompile(`[\t\n\r]+| {4,}`)

// TemplateParam limpia un parámetro: Meta rechaza saltos de línea, tabs y 4+ espacios seguidos.
func TemplateParam(s string) string {
	return core.Truncate(strings.TrimSpace(paramSpaces.ReplaceAllString(s, " · ")), 900)
}

func (c *Client) SendTemplate(ctx context.Context, to, name, lang string, params ...string) error {
	ps := make([]map[string]string, len(params))
	for i, p := range params {
		ps[i] = map[string]string{"type": "text", "text": TemplateParam(p)}
	}
	tpl := map[string]any{"name": name, "language": map[string]string{"code": lang}}
	if len(ps) > 0 {
		tpl["components"] = []map[string]any{{"type": "body", "parameters": ps}}
	}
	_, err := c.send(ctx, to, "template", name, map[string]any{"type": "template", "template": tpl})
	return err
}

// UploadMedia sube un archivo y devuelve su id para mandarlo.
func (c *Client) UploadMedia(ctx context.Context, data []byte, mime, filename string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("messaging_product", "whatsapp")
	_ = w.WriteField("type", mime)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	h.Set("Content-Type", mime)
	fw, _ := w.CreatePart(h)
	_, _ = fw.Write(data)
	_ = w.Close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+c.phoneID+"/media", &buf)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		ID    string `json:"id"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	if res.StatusCode >= 300 || out.ID == "" {
		return "", fmt.Errorf("whatsapp media %d: %s", res.StatusCode, out.Error.Message)
	}
	return out.ID, nil
}

// SendVoiceNote manda un audio como nota de voz (Ogg/Opus). Solo dentro de la ventana.
func (c *Client) SendVoiceNote(ctx context.Context, to string, audio []byte) error {
	id, err := c.UploadMedia(ctx, audio, "audio/ogg", "mano.ogg")
	if err != nil {
		return err
	}
	_, err = c.send(ctx, to, "audio", "", map[string]any{"type": "audio", "audio": map[string]any{"id": id, "voice": true}})
	return err
}

// WindowOpen dice si el contacto escribió en las últimas 24 h.
func (c *Client) WindowOpen(ctx context.Context, phone string) bool {
	if c.store == nil {
		return false
	}
	last, err := c.store.LastInbound(ctx, phone)
	return err == nil && !last.IsZero() && c.now().Sub(last) < Window
}

// ReplyInWindow responde libre a un cliente; falla si la ventana se cerró (no inventa una plantilla).
func (c *Client) ReplyInWindow(ctx context.Context, to, body string) error {
	if !c.WindowOpen(ctx, to) {
		return ErrWindowClosed
	}
	return c.SendText(ctx, to, body)
}

// Media baja un archivo entrante. La URL de Meta vence rápido: se baja en el momento.
func (c *Client) Media(ctx context.Context, mediaID string) ([]byte, string, error) {
	var meta struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
	}
	if err := c.do(ctx, http.MethodGet, c.base+"/"+mediaID, nil, &meta); err != nil {
		return nil, "", err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, "", fmt.Errorf("descarga de media: %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 25<<20))
	return b, meta.MimeType, err
}

// AudioFilename elige la extensión que el transcriptor necesita según el mime.
func AudioFilename(mime string) string {
	switch {
	case strings.Contains(mime, "ogg"), strings.Contains(mime, "opus"):
		return "nota.ogg"
	case strings.Contains(mime, "mpeg"), strings.Contains(mime, "mp3"):
		return "nota.mp3"
	case strings.Contains(mime, "mp4"), strings.Contains(mime, "m4a"), strings.Contains(mime, "aac"):
		return "nota.m4a"
	case strings.Contains(mime, "amr"):
		return "nota.amr"
	}
	return "nota.ogg"
}

// ── Canal del dueño ────────────────────────────────────────

// OwnerChannel implementa owner.Channel y agent.Notifier sobre el WhatsApp del dueño.
// Dentro de la ventana manda texto y botones; fuera, la plantilla utility de aviso.
type OwnerChannel struct {
	c                  *Client
	phone              string
	template, language string
}

func NewOwnerChannel(c *Client, phone, template, language string) *OwnerChannel {
	return &OwnerChannel{c: c, phone: phone, template: template, language: language}
}

func (o *OwnerChannel) notice(ctx context.Context, text string) error {
	if o.template == "" {
		return ErrWindowClosed
	}
	return o.c.SendTemplate(ctx, o.phone, o.template, o.language, text)
}

// SendVoice manda la nota de voz si la ventana está abierta; si no, el aviso por plantilla con el texto.
// Es un solo mensaje igual que el aviso de texto: la nota reemplaza al texto, no se suma.
func (o *OwnerChannel) SendVoice(ctx context.Context, audio []byte, caption string) error {
	if !o.c.WindowOpen(ctx, o.phone) {
		return o.notice(ctx, caption)
	}
	return o.c.SendVoiceNote(ctx, o.phone, audio)
}

func (o *OwnerChannel) Send(ctx context.Context, text string) error {
	if o.c.WindowOpen(ctx, o.phone) {
		return o.c.SendText(ctx, o.phone, text)
	}
	return o.notice(ctx, text)
}

var riskLabel = map[string]string{"bajo": "riesgo bajo", "medio": "riesgo medio", "alto": "RIESGO ALTO"}

func (o *OwnerChannel) ApprovalCard(ctx context.Context, a store.Approval) error {
	if !o.c.WindowOpen(ctx, o.phone) {
		// Los botones solo viajan dentro de la ventana. La plantilla avisa; al contestar, se abre y ve la tarjeta.
		return o.notice(ctx, fmt.Sprintf("Necesito tu ok: %s (%s). Respondé /pendientes para verlo.", a.Title, riskLabel[a.Risk]))
	}
	body := fmt.Sprintf("*Necesita tu ok* · %s · %s\n%s\n\n%s", a.Area, riskLabel[a.Risk], a.Title, a.Detail)
	return o.c.SendButtons(ctx, o.phone, body, []Button{
		{ID: "ap:" + a.ID + ":ok", Title: "Aprobar"},
		{ID: "ap:" + a.ID + ":no", Title: "Descartar"},
		{ID: "ap:" + a.ID + ":ig", Title: "Silenciar remitente"},
	})
}
