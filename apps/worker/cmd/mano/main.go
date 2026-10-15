// Mano worker: webhooks, bot de Telegram, resumen diario y renovación del watch de Gmail.
//
//	go run ./cmd/mano               # arranca todo
//	go run ./cmd/mano -gmail-watch  # registra el watch de Gmail y sale
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // zonas horarias embebidas: funciona igual en cualquier VPS o contenedor

	"github.com/nawesan12/mano/worker/internal/agent"
	"github.com/nawesan12/mano/worker/internal/config"
	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/gmail"
	"github.com/nawesan12/mano/worker/internal/schedule"
	"github.com/nawesan12/mano/worker/internal/server"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/telegram"
)

func main() {
	watchOnly := flag.Bool("gmail-watch", false, "registra el watch de Gmail y sale")
	flag.Parse()

	config.LoadDotenv(os.Getenv("MANO_ENV"), ".env", "../../.env")
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var gm *gmail.Client
	if cfg.GmailEnabled() {
		gm = gmail.New(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRefreshToken, cfg.GmailTopic)
	}
	if *watchOnly {
		if gm == nil {
			fmt.Fprintln(os.Stderr, "Gmail no está configurado en .env")
			os.Exit(1)
		}
		h, exp, err := gm.Watch(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("Watch activo. historyId %s · vence %s\n", h, exp.Format(time.RFC3339))
		return
	}

	st := store.New(cfg.SupabaseURL, cfg.SupabaseKey, cfg.TenantID)
	cheap := core.NewLLM(core.ModelConfig{Tier: core.Cheap, Provider: cfg.CheapProvider, Model: cfg.CheapModel, APIKey: cfg.CheapKey, BaseURL: cfg.CheapBaseURL, PriceIn: cfg.CheapPriceIn, PriceOut: cfg.CheapPriceOut})
	top := core.NewLLM(core.ModelConfig{Tier: core.Top, Provider: cfg.TopProvider, Model: cfg.TopModel, APIKey: cfg.TopKey, BaseURL: cfg.TopBaseURL, PriceIn: cfg.TopPriceIn, PriceOut: cfg.TopPriceOut})
	models := agent.NewModels(cheap, top, st, core.Caps{DayUSD: cfg.CapDayUSD, MonthUSD: cfg.CapMonthUSD})

	tg := telegram.NewClient(cfg.TelegramToken)
	notifier := telegram.NewNotifier(tg, cfg.OwnerChatID)
	models.OnHalt = func(reason string) { _ = notifier.Send(context.Background(), "Freno de gasto: "+reason) }

	var mailer agent.Mailer
	var source server.GmailSource
	if gm != nil {
		mailer, source = gm, gm
	}
	ag := agent.New(st, models, notifier, mailer, cfg.Location)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           (&server.Server{Gmail: source, Sink: ag, PushToken: cfg.GmailPushToken, TenantID: cfg.TenantID, Base: ctx}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("webhooks escuchando", "port", cfg.Port, "gmail", gm != nil)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http", "err", err)
			stop()
		}
	}()

	schedule.Daily(ctx, "resumen", cfg.BriefAt, cfg.Location, func(c context.Context) {
		text, err := ag.Brief(c)
		if err != nil {
			slog.Error("resumen", "err", err)
			return
		}
		_ = notifier.Send(c, text)
	})

	if gm != nil && cfg.GmailTopic != "" {
		renew := func(c context.Context) {
			if _, exp, err := gm.Watch(c); err != nil {
				slog.Error("gmail watch", "err", err)
			} else {
				slog.Info("gmail watch renovado", "vence", exp)
			}
		}
		renew(ctx)
		schedule.Daily(ctx, "gmail-watch", "03:00", cfg.Location, renew)
	}

	slog.Info("mano trabajando", "resumen", cfg.BriefAt, "tz", cfg.Location.String())
	telegram.NewBot(tg, cfg.OwnerChatID, ag, models, st, notifier, telegram.STT{BaseURL: cfg.STTBaseURL, Key: cfg.STTKey, Model: cfg.STTModel}).Run(ctx)

	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	slog.Info("mano detenido")
}
