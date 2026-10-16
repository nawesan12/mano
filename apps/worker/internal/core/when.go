package core

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// la hora va al final: "15", "15:30", "15hs", "a las 9", "9.30 hs"
var timeRe = regexp.MustCompile(`(?:\s+a\s+las|\s+las|\s+)\s*(\d{1,2})(?:[:.](\d{2}))?\s*(?:hs|h|horas)?$`)

// ParseDay entiende el día: "el jueves", "mañana", "hoy", "20/10", "2026-10-20". Devuelve las 00:00 de ese día en loc.
func ParseDay(s string, now time.Time, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(Norm(s))
	if s == "hoy" || s == "" {
		n := now.In(loc)
		return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc), nil
	}
	end, err := ParseUntil(s, now, loc)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc), nil
}

// ParseWhen entiende "el jueves a las 15", "mañana 9:30", "20/10 10hs". Devuelve la fecha y hora en loc.
func ParseWhen(s string, now time.Time, loc *time.Location) (time.Time, error) {
	n := Norm(strings.TrimSpace(s))
	m := timeRe.FindStringSubmatchIndex(n)
	if m == nil {
		return time.Time{}, fmt.Errorf("decime también la hora, por ejemplo \"el jueves a las 15\"")
	}
	h, _ := strconv.Atoi(n[m[2]:m[3]])
	min := 0
	if m[4] >= 0 {
		min, _ = strconv.Atoi(n[m[4]:m[5]])
	}
	if h > 23 || min > 59 {
		return time.Time{}, fmt.Errorf("esa hora no existe")
	}
	if h < 7 && !strings.Contains(n, "manana temprano") {
		h += 12 // "a las 3" en un negocio es a las 15
	}
	day, err := ParseDay(strings.TrimSpace(n[:m[0]]), now, loc)
	if err != nil {
		return time.Time{}, err
	}
	t := time.Date(day.Year(), day.Month(), day.Day(), h, min, 0, 0, loc)
	if !t.After(now) {
		return time.Time{}, fmt.Errorf("ese horario ya pasó")
	}
	return t, nil
}

// Slot es un hueco libre.
type Slot struct{ From, To time.Time }

// FreeSlots calcula los huecos libres de un día entre workFrom y workTo (horas), sacando lo ocupado. Sin IA.
func FreeSlots(day time.Time, busy []Slot, workFrom, workTo int, minLen time.Duration) []Slot {
	start := time.Date(day.Year(), day.Month(), day.Day(), workFrom, 0, 0, 0, day.Location())
	end := time.Date(day.Year(), day.Month(), day.Day(), workTo, 0, 0, 0, day.Location())
	sorted := append([]Slot(nil), busy...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].From.Before(sorted[j].From) })
	var out []Slot
	cur := start
	add := func(to time.Time) {
		if to.Sub(cur) >= minLen {
			out = append(out, Slot{cur, to})
		}
	}
	for _, b := range sorted {
		if !b.To.After(cur) {
			continue
		}
		if !b.From.Before(end) {
			break
		}
		if b.From.After(cur) {
			add(b.From)
		}
		cur = b.To
	}
	if cur.Before(end) {
		add(end)
	}
	return out
}
