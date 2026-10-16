package whatsapp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/nawesan12/mano/worker/internal/core"
)

// ErrNoCallPermission: el usuario no le dio permiso de llamada al número del negocio (código 138006).
var ErrNoCallPermission = errors.New("el dueño todavía no le dio permiso de llamada a Mano en WhatsApp")

// CallSession es la descripción WebRTC que viaja en las llamadas (RFC 8866).
type CallSession struct {
	SDPType string `json:"sdp_type"`
	SDP     string `json:"sdp"`
}

func (c *Client) calls(ctx context.Context, payload map[string]any, out any) error {
	payload["messaging_product"] = "whatsapp"
	err := c.do(ctx, http.MethodPost, c.base+"/"+c.phoneID+"/calls", payload, out)
	if err != nil && strings.Contains(err.Error(), "código 138006") {
		return ErrNoCallPermission
	}
	return err
}

// StartCall llama a un usuario con nuestra oferta SDP. La respuesta SDP llega después por el webhook (evento connect).
func (c *Client) StartCall(ctx context.Context, to, sdpOffer, opaque string) (string, error) {
	var out struct {
		Calls []struct {
			ID string `json:"id"`
		} `json:"calls"`
	}
	err := c.calls(ctx, map[string]any{
		"to": to, "action": "connect", "session": CallSession{SDPType: "offer", SDP: sdpOffer},
		"biz_opaque_callback_data": core.Truncate(opaque, 512),
	}, &out)
	if err != nil {
		return "", err
	}
	if len(out.Calls) == 0 || out.Calls[0].ID == "" {
		return "", errors.New("whatsapp no devolvió id de llamada")
	}
	return out.Calls[0].ID, nil
}

// AnswerCall atiende una llamada entrante: pre_accept arma el audio antes de conectar; accept la conecta.
func (c *Client) AnswerCall(ctx context.Context, callID, action, sdpAnswer string) error {
	return c.calls(ctx, map[string]any{"call_id": callID, "action": action, "session": CallSession{SDPType: "answer", SDP: sdpAnswer}}, nil)
}

func (c *Client) RejectCall(ctx context.Context, callID string) error {
	return c.calls(ctx, map[string]any{"call_id": callID, "action": "reject"}, nil)
}

func (c *Client) TerminateCall(ctx context.Context, callID string) error {
	return c.calls(ctx, map[string]any{"call_id": callID, "action": "terminate"}, nil)
}

// RequestCallPermission pide permiso de llamada. Dentro de la ventana va como interactivo; fuera, solo con plantilla
// (que tiene que incluir el componente call_permission_request). Sin plantilla y con la ventana cerrada, ErrWindowClosed.
func (c *Client) RequestCallPermission(ctx context.Context, to, body, template, lang string) error {
	if c.WindowOpen(ctx, to) {
		_, err := c.send(ctx, to, "interactive", "", map[string]any{"type": "interactive", "interactive": map[string]any{
			"type": "call_permission_request", "action": map[string]string{"name": "call_permission_request"},
			"body": map[string]string{"text": core.Truncate(body, 1024)},
		}})
		return err
	}
	if template == "" {
		return ErrWindowClosed
	}
	return c.SendTemplate(ctx, to, template, lang)
}
