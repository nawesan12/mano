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
type Models struct {
	Cheap, Top core.LLM
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
