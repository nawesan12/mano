package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CallPolicy define cuándo Mano puede llamar al dueño. Se guarda en la tabla call_policy.
type CallPolicy struct {
	Enabled       bool   `json:"enabled"`
	QuietFrom     string `json:"quiet_from"` // "21:00"
	QuietTo       string `json:"quiet_to"`   // "08:00"
	MaxPerDay     int    `json:"max_per_day"`
	RetryAfterMin int    `json:"retry_after_min"`
	MaxAttempts   int    `json:"max_attempts"`
}

func DefaultCallPolicy() CallPolicy {
	return CallPolicy{Enabled: true, QuietFrom: "21:00", QuietTo: "08:00", MaxPerDay: 3, RetryAfterMin: 10, MaxAttempts: 2}
}

// CallRequest es el motivo de una llamada y lo que la habilita.
type CallRequest struct {
	Urgency     Urgency
	Forced      bool // la regla del dueño pide llamar (Notify == call)
	IgnoreQuiet bool // la regla del dueño deja llamar en horario de silencio
	OwnerAsked  bool // el dueño pidió que lo llame ya (/llamame)
}

type CallDecision struct {
	Call   bool
	Reason string
}

// MinutesOfDay convierte "HH:MM" en minutos desde la medianoche.
func MinutesOfDay(hhmm string) (int, error) {
	h, m, ok := strings.Cut(hhmm, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("hora inválida %q, usá HH:MM", hhmm)
	}
	return hh*60 + mm, nil
}

// InQuiet dice si now cae en el horario de silencio. Soporta rangos que cruzan la medianoche (21:00 → 08:00).
func (p CallPolicy) InQuiet(now time.Time) bool {
	from, err1 := MinutesOfDay(p.QuietFrom)
	to, err2 := MinutesOfDay(p.QuietTo)
	if err1 != nil || err2 != nil || from == to {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	if from < to {
		return cur >= from && cur < to
	}
	return cur >= from || cur < to
}

// ShouldCall decide sin IA si corresponde llamar. now tiene que venir en la zona horaria del dueño.
// recentMissed = llamadas seguidas sin atender: WhatsApp revoca el permiso a las 4, cortamos en 2.
func ShouldCall(p CallPolicy, r CallRequest, now time.Time, callsToday, recentMissed int) CallDecision {
	if r.OwnerAsked {
		return CallDecision{true, "lo pediste"}
	}
	if !p.Enabled {
		return CallDecision{false, "las llamadas están apagadas"}
	}
	if r.Urgency != Critical && !r.Forced {
		return CallDecision{false, "no es crítico"}
	}
	if p.InQuiet(now) && !r.IgnoreQuiet {
		return CallDecision{false, fmt.Sprintf("es horario de silencio (%s a %s)", p.QuietFrom, p.QuietTo)}
	}
	if callsToday >= p.MaxPerDay {
		return CallDecision{false, fmt.Sprintf("ya te llamé %d veces hoy", callsToday)}
	}
	if recentMissed >= 2 {
		return CallDecision{false, "no atendiste las dos últimas llamadas"}
	}
	return CallDecision{true, "crítico"}
}

// ParseQuiet entiende "21-8", "21:30-07:00" o "22 a 7". Devuelve (desde, hasta).
func ParseQuiet(s string) (string, string, error) {
	s = strings.NewReplacer(" a ", "-", " ", "", "hs", "", "h", "").Replace(strings.ToLower(strings.TrimSpace(s)))
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return "", "", fmt.Errorf("usalo así: /silencio 21-8")
	}
	norm := func(x string) (string, error) {
		if !strings.Contains(x, ":") {
			x += ":00"
		}
		m, err := MinutesOfDay(x)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%02d:%02d", m/60, m%60), nil
	}
	from, err := norm(a)
	if err != nil {
		return "", "", err
	}
	to, err := norm(b)
	if err != nil {
		return "", "", err
	}
	return from, to, nil
}
