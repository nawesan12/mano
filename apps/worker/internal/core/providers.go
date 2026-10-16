package core

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
)

// Providers: con el nombre alcanza; la URL sale sola. Todos menos Anthropic hablan el formato de OpenAI.
// Verificar la URL vigente de cada proveedor si alguno cambia su API.
var Providers = map[string]string{
	"anthropic":  "https://api.anthropic.com",
	"openai":     "https://api.openai.com/v1",
	"gemini":     "https://generativelanguage.googleapis.com/v1beta/openai",
	"deepseek":   "https://api.deepseek.com",
	"openrouter": "https://openrouter.ai/api/v1",
	"groq":       "https://api.groq.com/openai/v1",
	"mistral":    "https://api.mistral.ai/v1",
	"xai":        "https://api.x.ai/v1",
	"together":   "https://api.together.xyz/v1",
	"fireworks":  "https://api.fireworks.ai/inference/v1",
	"cerebras":   "https://api.cerebras.ai/v1",
	"ollama":     "http://localhost:11434/v1",
	// cualquier otro compatible con OpenAI: con su BASE_URL
	"openai-compatible": "https://api.openai.com/v1",
}

// ProviderNames lista los proveedores válidos, para los mensajes de configuración.
func ProviderNames() string {
	names := make([]string, 0, len(Providers))
	for n := range Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// HTTPError es una respuesta de error del proveedor.
type HTTPError struct {
	Status int
	Msg    string
}

func (e *HTTPError) Error() string { return e.Msg }

// Retryable: vale probar con otro proveedor si este está caído, saturado, sin saldo o rechaza la key
// (el otro tiene la suya). Un 400 es un pedido mal armado: otro proveedor tampoco lo va a aceptar.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	var h *HTTPError
	if errors.As(err, &h) {
		return h.Status == 401 || h.Status == 402 || h.Status == 403 || h.Status == 404 || h.Status == 408 || h.Status == 429 || h.Status >= 500
	}
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded)
}

// Fallback prueba los modelos en orden: si uno falla por algo que otro puede resolver, sigue con el próximo.
// El resultado lleva el modelo y el costo de quien respondió: así el gobernador registra lo real.
type Fallback struct {
	chain []LLM
}

func WithFallback(primary LLM, backups ...LLM) LLM {
	chain := []LLM{primary}
	for _, b := range backups {
		if b != nil {
			chain = append(chain, b)
		}
	}
	if len(chain) == 1 {
		return primary
	}
	return &Fallback{chain: chain}
}

func (f *Fallback) Tier() Tier    { return f.chain[0].Tier() }
func (f *Fallback) Model() string { return f.chain[0].Model() }

func (f *Fallback) Complete(ctx context.Context, r Request) (Result, error) {
	var last error
	for _, m := range f.chain {
		res, err := m.Complete(ctx, r)
		if err == nil {
			res.Tier = f.Tier()
			return res, nil
		}
		last = err
		if !Retryable(err) || ctx.Err() != nil {
			return res, err
		}
	}
	return Result{}, last
}
