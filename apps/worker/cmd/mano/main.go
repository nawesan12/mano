// Mano worker: webhooks (Gmail, WhatsApp), bot de Telegram, resumen diario y renovación del watch de Gmail.
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
	"github.com/nawesan12/mano/worker/internal/calendar"
	"github.com/nawesan12/mano/worker/internal/config"
	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/gmail"
	"github.com/nawesan12/mano/worker/internal/google"
	"github.com/nawesan12/mano/worker/internal/mercadopago"
	"github.com/nawesan12/mano/worker/internal/owner"
	"github.com/nawesan12/mano/worker/internal/schedule"
	"github.com/nawesan12/mano/worker/internal/server"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/stt"
	"github.com/nawesan12/mano/worker/internal/telegram"
	"github.com/nawesan12/mano/worker/internal/tts"
	"github.com/nawesan12/mano/worker/internal/voice"
	"github.com/nawesan12/mano/worker/internal/whatsapp"
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
	transcriber := stt.New(cfg.STTBaseURL, cfg.STTKey, cfg.STTModel)
	if speaker := tts.New(cfg.TTSBaseURL, cfg.TTSKey, cfg.TTSModel, cfg.TTSVoice, cfg.TTSPricePerMChars); speaker != nil {
		models.TTS = speaker // lo importante llega como nota de voz
	}

	// Canales hacia el dueño. El preferido primero; el otro como respaldo.
	var tgClient *telegram.Client
	var tgChan *telegram.Channel
	var waClient *whatsapp.Client
	var waChan *whatsapp.OwnerChannel
	if cfg.TelegramEnabled() {
		tgClient = telegram.NewClient(cfg.TelegramToken)
		tgChan = telegram.NewChannel(tgClient, cfg.OwnerChatID)
	}
	if cfg.WhatsAppEnabled() {
		waClient = whatsapp.NewClient(cfg.WAPhoneID, cfg.WAToken, cfg.WAAPIVersion, st)
		waChan = whatsapp.NewOwnerChannel(waClient, cfg.WAOwnerPhone, cfg.WATemplateNotice, cfg.WATemplateLang)
	}
	var notifier owner.Fallback
	add := func(c owner.Channel) {
		if c != nil {
			notifier = append(notifier, c)
		}
	}
	if cfg.OwnerChannel == "whatsapp" {
		add(chanOrNil(waChan))
		add(chanOrNil(tgChan))
	} else {
		add(chanOrNil(tgChan))
		add(chanOrNil(waChan))
	}
	models.OnHalt = func(reason string) { _ = notifier.Send(context.Background(), "Freno de gasto: "+reason) }

	var mailer agent.Mailer
	var source server.GmailSource
	if gm != nil {
		mailer, source = gm, gm
	}
	var waReplier agent.WhatsApper
	if waClient != nil {
		waReplier = waClient
	}
	ag := agent.New(st, models, notifier, mailer, waReplier, cfg.Location)
	svc := &owner.Service{Agent: ag, Models: models, Store: st, STT: transcriber}

	// Voz: lo crítico te llama por WhatsApp y lo resolvés hablando.
	var caller *voice.Caller
	if cfg.VoiceEnabled && waClient != nil {
		var stun []string
		if cfg.STUNURL != "" {
			stun = []string{cfg.STUNURL}
		}
		caller = &voice.Caller{
			Phone: waClient, Brain: voice.NewRealtime(cfg.RealtimeBaseURL, cfg.RealtimeKey),
			NewMedia: func() (voice.Media, error) { return voice.NewBridge(stun) },
			Store:    st, Models: models, Decider: ag, Notify: notifier,
			OwnerPhone: cfg.WAOwnerPhone, Loc: cfg.Location,
			Model: cfg.RealtimeModel, Voice: cfg.RealtimeVoice, TranscribeModel: cfg.RealtimeTranscribe,
			PriceIn: cfg.RealtimePriceIn, PriceOut: cfg.RealtimePriceOut, WAUSDPerMin: cfg.WACallUSDPerMin,
			MaxCall: cfg.CallMax, RingTimeout: cfg.CallRing,
			PermissionTemplate: cfg.WATemplateCallPermission, TemplateLang: cfg.WATemplateLang,
		}
		ag.SetCaller(caller)
		svc.Calls = caller
	}

	srvCfg := &server.Server{Gmail: source, Sink: ag, PushToken: cfg.GmailPushToken, TenantID: cfg.TenantID, Base: ctx}
	if waClient != nil {
		wh := &whatsapp.Webhook{
			Client: waClient, Store: st, Owner: svc, OwnerChan: waChan, Sink: ag, STT: transcriber,
			AppSecret: cfg.WAAppSecret, VerifyToken: cfg.WAVerifyToken, PhoneID: cfg.WAPhoneID,
			OwnerPhone: cfg.WAOwnerPhone, TenantID: cfg.TenantID, Base: ctx,
		}
		if caller != nil {
			wh.Calls = caller
		}
		srvCfg.WhatsApp = wh
	}
	srvCfg.Hooks = map[string]http.Handler{}
	if cfg.MPAccessToken != "" {
		srvCfg.Hooks["/webhooks/mercadopago"] = &mercadopago.Webhook{Client: mercadopago.NewClient(cfg.MPAccessToken),
			Secret: cfg.MPWebhookSecret, Sink: ag, TenantID: cfg.TenantID, Base: ctx}
	}
	var cal *calendar.Service
	if cfg.CalendarEnabled {
		cal = &calendar.Service{
			Client: calendar.NewClient(google.NewAuth(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRefreshToken)),
			Store:  st, Sink: ag, Loc: cfg.Location, TenantID: cfg.TenantID, PushToken: cfg.CalendarPushToken,
			Address: cfg.PublicURL + "/webhooks/calendar", Base: ctx,
		}
		srvCfg.Hooks["/webhooks/calendar"] = cal
	}
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: srvCfg.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		slog.Info("webhooks escuchando", "port", cfg.Port, "gmail", gm != nil, "whatsapp", waClient != nil, "mercadopago", cfg.MPAccessToken != "", "calendar", cfg.CalendarEnabled)
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
		if err := notifier.Send(c, text); err != nil {
			slog.Error("resumen: no pude avisar", "err", err)
		}
	})

	if cfg.CloseAt != "" {
		schedule.Daily(ctx, "cierre", cfg.CloseAt, cfg.Location, func(c context.Context) {
			text, err := ag.Closing(c)
			if err == nil {
				err = notifier.Send(c, text)
			}
			if err != nil {
				slog.Error("cierre", "err", err)
			}
		})
	}

	// Seguimiento: lo que espera tu ok se recuerda una vez y vence a las 72 h.
	schedule.Every(ctx, "seguimiento", 30*time.Minute, func(c context.Context) {
		text, err := ag.FollowUp(c, time.Now())
		if err == nil && text != "" {
			err = notifier.Send(c, text)
		}
		if err != nil {
			slog.Error("seguimiento", "err", err)
		}
	})

	if cal != nil {
		// Primero se llena la copia local (sin avisar), después se pide el push. Google corta el canal a los ~7 días.
		start := func(c context.Context) {
			if err := cal.Sync(c); err != nil {
				slog.Error("calendar sync", "err", err)
			}
			if err := cal.Renew(c); err != nil {
				slog.Error("calendar watch", "err", err)
			}
		}
		go start(ctx)
		schedule.Daily(ctx, "calendar-watch", "03:10", cfg.Location, start)
	}

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

	slog.Info("mano trabajando", "canal", cfg.OwnerChannel, "llamadas", caller != nil, "resumen", cfg.BriefAt, "tz", cfg.Location.String())
	if tgClient != nil {
		telegram.NewBot(tgClient, tgChan, cfg.OwnerChatID, svc).Run(ctx)
	} else {
		<-ctx.Done()
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	slog.Info("mano detenido")
}

// chanOrNil evita meter un puntero nil tipado dentro de la interfaz.
func chanOrNil[T owner.Channel](c T) owner.Channel {
	var zero T
	if any(c) == any(zero) {
		return nil
	}
	return c
}
