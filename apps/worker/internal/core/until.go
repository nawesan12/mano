package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	daysRe = regexp.MustCompile(`^(\d{1,2})\s*(dias|dia|d)$`)
	dateRe = regexp.MustCompile(`^(\d{1,2})[/-](\d{1,2})(?:[/-](\d{2,4}))?$`)
	isoRe  = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
)

var weekdays = map[string]time.Weekday{
	"domingo": time.Sunday, "lunes": time.Monday, "martes": time.Tuesday, "miercoles": time.Wednesday,
	"jueves": time.Thursday, "viernes": time.Friday, "sabado": time.Saturday,
}

// ParseUntil entiende "el lunes", "20/10", "2026-10-20", "3 días", "mañana". Devuelve el fin de ese día
// (23:59 en loc): "hasta el lunes" incluye el lunes entero.
func ParseUntil(s string, now time.Time, loc *time.Location) (time.Time, error) {
	n := now.In(loc)
	s = strings.TrimSpace(Norm(s))
	s = strings.TrimPrefix(s, "hasta ")
	s = strings.TrimPrefix(s, "el ")
	endOf := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 23, 59, 0, 0, loc) }
	var t time.Time
	switch {
	case s == "manana":
		t = endOf(n.Year(), n.Month(), n.Day()+1)
	case s == "hoy":
		t = endOf(n.Year(), n.Month(), n.Day())
	case daysRe.MatchString(s):
		k, _ := strconv.Atoi(daysRe.FindStringSubmatch(s)[1])
		t = endOf(n.Year(), n.Month(), n.Day()+k)
	case isoRe.MatchString(s):
		m := isoRe.FindStringSubmatch(s)
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		t = endOf(y, time.Month(mo), d)
		if t.Day() != d {
			return time.Time{}, fmt.Errorf("esa fecha no existe")
		}
	case dateRe.MatchString(s):
		m := dateRe.FindStringSubmatch(s)
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y := n.Year()
		if m[3] != "" {
			y, _ = strconv.Atoi(m[3])
			if y < 100 {
				y += 2000
			}
		}
		t = endOf(y, time.Month(mo), d)
		if t.Day() != d {
			return time.Time{}, fmt.Errorf("esa fecha no existe")
		}
		if m[3] == "" && t.Before(n) { // "5/1" en diciembre = el año que viene
			t = endOf(y+1, time.Month(mo), d)
		}
	default:
		wd, ok := weekdays[s]
		if !ok {
			return time.Time{}, fmt.Errorf("no entendí la fecha %q: probá con \"el lunes\", \"20/10\" o \"3 días\"", s)
		}
		k := (int(wd) - int(n.Weekday()) + 7) % 7
		if k == 0 {
			k = 7 // "hasta el lunes" dicho un lunes = el que viene
		}
		t = endOf(n.Year(), n.Month(), n.Day()+k)
	}
	if !t.After(n) {
		return time.Time{}, fmt.Errorf("esa fecha ya pasó")
	}
	if t.Sub(n) > 60*24*time.Hour {
		return time.Time{}, fmt.Errorf("como máximo 60 días")
	}
	return t, nil
}
