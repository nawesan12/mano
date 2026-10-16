// Package schedule corre tareas diarias a una hora local, sin dependencias.
package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Next devuelve la próxima ocurrencia de HH:MM en loc posterior a now.
func Next(now time.Time, hhmm string, loc *time.Location) (time.Time, error) {
	var h, m int
	if _, err := fmt.Sscanf(hhmm, "%d:%d", &h, &m); err != nil {
		return time.Time{}, err
	}
	n := now.In(loc)
	t := time.Date(n.Year(), n.Month(), n.Day(), h, m, 0, 0, loc)
	if !t.After(n) {
		t = time.Date(n.Year(), n.Month(), n.Day()+1, h, m, 0, 0, loc)
	}
	return t, nil
}

// Daily corre fn todos los días a HH:MM hasta que se cancele el contexto.
func Daily(ctx context.Context, name, hhmm string, loc *time.Location, fn func(context.Context)) {
	go func() {
		for {
			next, err := Next(time.Now(), hhmm, loc)
			if err != nil {
				slog.Error("schedule", "name", name, "err", err)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
				run(ctx, name, fn)
			}
		}
	}()
}

func run(ctx context.Context, name string, fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("tarea programada", "name", name, "panic", r)
		}
	}()
	c, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	fn(c)
}

// Every corre fn cada interval (la primera vez después de un interval) hasta que se cancele el contexto.
func Every(ctx context.Context, name string, interval time.Duration, fn func(context.Context)) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				run(ctx, name, fn)
			}
		}
	}()
}
