package agent

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

// Blocked es el error cuando el gobernador frena una llamada.
type Blocked struct{ Reason string }

func (b *Blocked) Error() string { return b.Reason }

func IsBlocked(err error) (*Blocked, bool) {
	var b *Blocked
	ok := errors.As(err, &b)
	return b, ok
}

// Models es el único punto de acceso a la IA: todo pasa por el gobernador
// y todo queda registrado en usage.
// Synth convierte texto en una nota de voz (tts.Client). nil = notas de voz apagadas.
type Synth interface {
	Model() string
	Estimate(text string) float64
	Synthesize(ctx context.Context, text string) ([]byte, float64, error)
}

type Models struct {
	Cheap, Top core.LLM
	TTS        Synth
	Embedder   core.Embedder // nil = memoria por palabras
	store      *store.Store
	fallback   core.Caps
	OnHalt     func(reason string)
	halted     atomic.Bool
}

func NewModels(cheap, top core.LLM, s *store.Store, fallback core.Caps) *Models {
	return &Models{Cheap: cheap, Top: top, store: s, fallback: fallback}
}

func (m *Models) Status(ctx context.Context) (core.Spend, core.Caps, error) {
	sp, err := m.store.Spend(ctx)
	return sp, m.store.Caps(ctx, m.fallback), err
}

func (m *Models) notify(d core.Decision) {
	if d.Mode != core.ModeHalted {
		m.halted.Store(false)
		return
	}
	if m.halted.CompareAndSwap(false, true) && m.OnHalt != nil {
		m.OnHalt(d.Reason)
	}
}

// CheapIfAllowed devuelve el modelo barato si el presupuesto lo permite; si no, nil
// (la cascada sigue sin IA y el evento queda para el resumen).
func (m *Models) CheapIfAllowed(ctx context.Context, critical bool) core.LLM {
	sp, caps, err := m.Status(ctx)
	if err != nil {
		return nil // sin datos de gasto, no se arriesga
	}
	d := core.Decide(core.Cheap, critical, -1, sp, caps)
	m.notify(d)
	if !d.OK {
		return nil
	}
	return m.Cheap
}

type CallOpts struct {
	Critical bool
	EstUSD   float64 // < 0 = sin estimación
	TaskID   string
}

func (m *Models) Complete(ctx context.Context, want core.Tier, req core.Request, o CallOpts) (core.Result, error) {
	sp, caps, err := m.Status(ctx)
	if err != nil {
		return core.Result{}, err
	}
	if o.EstUSD == 0 {
		o.EstUSD = -1
	}
	d := core.Decide(want, o.Critical, o.EstUSD, sp, caps)
	m.notify(d)
	if !d.OK {
		return core.Result{}, &Blocked{d.Reason}
	}
	client := m.Cheap
	if d.Tier == core.Top {
		client = m.Top
	}
	r, err := client.Complete(ctx, req)
	if err != nil {
		return r, err
	}
	_ = m.store.RecordUsage(ctx, r, o.TaskID)
	return r, nil
}

// AuthorizeVoice pasa una llamada de voz por el gobernador antes de marcar. estUSD es el peor caso (llamada al máximo).
func (m *Models) AuthorizeVoice(ctx context.Context, estUSD float64, critical bool) error {
	return m.authorize(ctx, core.Voice, estUSD, critical)
}

// Speak arma una nota de voz pasando por el gobernador y registra su costo.
func (m *Models) Speak(ctx context.Context, text string, critical bool) ([]byte, error) {
	if m.TTS == nil {
		return nil, errors.New("las notas de voz no están configuradas")
	}
	if err := m.authorize(ctx, core.Voice, m.TTS.Estimate(text), critical); err != nil {
		return nil, err
	}
	audio, cost, err := m.TTS.Synthesize(ctx, text)
	if err != nil {
		return nil, err
	}
	_ = m.store.RecordUsage(ctx, core.Result{Tier: core.Voice, Model: m.TTS.Model(), CostUSD: cost}, "")
	return audio, nil
}

func (m *Models) authorize(ctx context.Context, tier core.Tier, estUSD float64, critical bool) error {
	sp, caps, err := m.Status(ctx)
	if err != nil {
		return err
	}
	d := core.Decide(tier, critical, estUSD, sp, caps)
	m.notify(d)
	if !d.OK {
		return &Blocked{d.Reason}
	}
	return nil
}

// RecordVoice registra en usage lo que costó la voz de una llamada.
func (m *Models) RecordVoice(ctx context.Context, model string, in, out int, costUSD float64) error {
	return m.store.RecordUsage(ctx, core.Result{Tier: core.Voice, Model: model, InputTokens: in, OutputTokens: out, CostUSD: costUSD}, "")
}

// Embed pasa los embeddings por el gobernador (como el modelo barato) y registra su costo.
func (m *Models) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if m.Embedder == nil {
		return nil, errors.New("sin modelo de embeddings")
	}
	if err := m.authorize(ctx, core.Cheap, -1, false); err != nil {
		return nil, err
	}
	vecs, r, err := m.Embedder.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	_ = m.store.RecordUsage(ctx, r, "")
	return vecs, nil
}
