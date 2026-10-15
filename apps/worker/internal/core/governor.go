package core

import (
	"fmt"
	"math"
)

type Spend struct {
	DayUSD, MonthUSD, HourUSD float64
}

type Caps struct {
	DayUSD, MonthUSD float64
	// Si en una hora se gasta más que esta fracción del tope diario, se corta (loop o spam). 0 = 0.5.
	AnomalyHourShare float64
}

type Mode string

const (
	ModeNormal       Mode = "normal"
	ModeDegraded     Mode = "degraded"
	ModeCriticalOnly Mode = "critical-only"
	ModeHalted       Mode = "halted"
)

type Decision struct {
	OK     bool
	Tier   Tier
	Mode   Mode
	Reason string
}

// GovernorMode: <80% normal · ≥80% degradado · ≥100% solo lo crítico · gasto anómalo en la hora: frenado.
func GovernorMode(s Spend, c Caps) Mode {
	share := c.AnomalyHourShare
	if share == 0 {
		share = 0.5
	}
	if s.HourUSD >= c.DayUSD*share {
		return ModeHalted
	}
	used := math.Max(s.DayUSD/c.DayUSD, s.MonthUSD/c.MonthUSD)
	switch {
	case used >= 1:
		return ModeCriticalOnly
	case used >= 0.8:
		return ModeDegraded
	default:
		return ModeNormal
	}
}

// Decide si una llamada puede hacerse y con qué modelo. estUSD < 0 = sin estimación.
func Decide(want Tier, critical bool, estUSD float64, s Spend, c Caps) Decision {
	m := GovernorMode(s, c)
	switch {
	case m == ModeHalted:
		return Decision{false, want, m, "Gasto anómalo en la última hora: frené todo y te aviso."}
	case m == ModeCriticalOnly && !critical:
		return Decision{false, want, m, "Llegaste al tope: solo atiendo lo crítico."}
	case m == ModeCriticalOnly:
		return Decision{true, Cheap, m, "Tope alcanzado: lo crítico va con el modelo barato."}
	case m == ModeDegraded && !critical:
		return Decision{true, Cheap, m, "Cerca del tope: uso el modelo barato."}
	}
	remaining := c.DayUSD - s.DayUSD
	if estUSD >= 0 && estUSD > remaining && !critical {
		return Decision{false, want, m, fmt.Sprintf("Esta tarea cuesta unos USD %.2f y te quedan %.2f hoy.", estUSD, remaining)}
	}
	return Decision{true, want, m, "ok"}
}
