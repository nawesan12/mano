package core

import (
	"context"
	"errors"
	"strings"
)

// EmbedDims tiene que coincidir con memory_chunks.embedding (vector(1024)).
const EmbedDims = 1024

// Embedder convierte textos en vectores para buscar por significado.
type Embedder interface {
	Model() string
	Embed(ctx context.Context, texts []string) ([][]float32, Result, error)
}

type embedder struct {
	provider, base, key, model string
	pricePerM                  float64
}

// NewEmbedder usa /embeddings con el formato de OpenAI (OpenAI, Gemini, Mistral, Together, Ollama…).
// Pide 1024 dimensiones; un modelo que no lo soporte falla en make check.
func NewEmbedder(provider, baseURL, key, model string, pricePerM float64) Embedder {
	if baseURL == "" {
		baseURL = Providers[provider]
	}
	if baseURL == "" {
		baseURL = Providers["openai"]
	}
	return &embedder{provider: provider, base: strings.TrimRight(baseURL, "/"), key: key, model: model, pricePerM: pricePerM}
}

func (e *embedder) Model() string { return e.model }

func (e *embedder) Embed(ctx context.Context, texts []string) ([][]float32, Result, error) {
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
		Usage struct {
			Prompt int `json:"prompt_tokens"`
			Total  int `json:"total_tokens"`
		} `json:"usage"`
	}
	headers := map[string]string{}
	if e.key != "" {
		headers["Authorization"] = "Bearer " + e.key
	}
	body := map[string]any{"model": e.model, "input": texts, "dimensions": EmbedDims}
	if err := postJSON(ctx, e.base+"/embeddings", headers, body, &out); err != nil {
		return nil, Result{}, err
	}
	if len(out.Data) != len(texts) {
		return nil, Result{}, errors.New("el proveedor devolvió menos vectores que textos")
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if len(d.Embedding) != EmbedDims {
			return nil, Result{}, errors.New("el modelo de embeddings no devuelve 1024 dimensiones: elegí uno que acepte dimensions=1024")
		}
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	tokens := out.Usage.Prompt
	if tokens == 0 {
		tokens = out.Usage.Total
	}
	return vecs, Result{Model: e.model, Tier: Cheap, InputTokens: tokens, CostUSD: float64(tokens) * e.pricePerM / 1e6}, nil
}
