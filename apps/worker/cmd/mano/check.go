package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/config"
	"github.com/nawesan12/mano/worker/internal/google"
	"github.com/nawesan12/mano/worker/internal/mercadopago"
	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/telegram"
	"github.com/nawesan12/mano/worker/internal/tts"
	"github.com/nawesan12/mano/worker/internal/voice"
	"github.com/nawesan12/mano/worker/internal/whatsapp"
)

// runCheck prueba cada integración configurada contra el servicio real y dice qué falla. No manda mensajes ni llama.
func runCheck(cfg config.Config) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ok := true
	report := func(name string, err error, detail string) {
		if err != nil {
			ok = false
			fmt.Printf("  FALLA  %-22s %v\n", name, err)
			return
		}
		fmt.Printf("  ok     %-22s %s\n", name, detail)
	}
	skip := func(name, why string) { fmt.Printf("  —      %-22s %s\n", name, why) }
	fmt.Println("Chequeo de Mano")

	st := store.New(cfg.SupabaseURL, cfg.SupabaseKey, cfg.TenantID)
	_, err := st.Spend(ctx)
	report("Supabase", err, "responde")
	if err == nil {
		_, err = st.CallPolicy(ctx)
		report("Migraciones", err, "tabla call_policy presente")
	}

	if cfg.TelegramEnabled() {
		user, err := telegram.NewClient(cfg.TelegramToken).Me(ctx)
		report("Telegram", err, "@"+user)
	} else {
		skip("Telegram", "sin configurar")
	}

	if cfg.WhatsAppEnabled() {
		wa := whatsapp.NewClient(cfg.WAPhoneID, cfg.WAToken, cfg.WAAPIVersion, nil)
		num, name, err := wa.PhoneInfo(ctx)
		report("WhatsApp", err, name+" · "+num)
		if err == nil {
			perm, _ := st.CallPermissionOf(ctx, cfg.WAOwnerPhone)
			if perm.Valid(time.Now()) {
				report("Permiso de llamada", nil, perm.Kind)
			} else {
				skip("Permiso de llamada", "sin registrar todavía: mandale /llamame y aceptá el pedido")
			}
		}
	} else {
		skip("WhatsApp", "sin configurar")
	}

	if cfg.VoiceEnabled {
		b, err := voice.NewBridge([]string{cfg.STUNURL})
		if err != nil {
			report("WebRTC", err, "")
		} else {
			offer, err := b.BrainOffer(ctx)
			if err == nil && !publicCandidate(offer) {
				err = fmt.Errorf("no descubrí la IP pública con %s: revisá UDP saliente o STUN_URL", cfg.STUNURL)
			}
			report("WebRTC / STUN", err, "IP pública descubierta")
			if err == nil {
				rt := voice.NewRealtime(cfg.RealtimeBaseURL, cfg.RealtimeKey)
				answer, err := rt.Connect(ctx, offer, map[string]any{"type": "realtime", "model": cfg.RealtimeModel, "instructions": "Chequeo. No digas nada."})
				if err == nil && !strings.HasPrefix(strings.TrimSpace(answer), "v=0") {
					err = fmt.Errorf("la respuesta no es SDP: %.80s", answer)
				}
				report("Voz en tiempo real", err, cfg.RealtimeModel+" acepta la conexión")
			}
			_ = b.Close()
		}
	} else {
		skip("Llamadas", "VOICE_ENABLED=false")
	}

	if speaker := tts.New(cfg.TTSBaseURL, cfg.TTSKey, cfg.TTSModel, cfg.TTSVoice, cfg.TTSPricePerMChars); speaker != nil {
		audio, _, err := speaker.Synthesize(ctx, "Hola.")
		report("Notas de voz", err, fmt.Sprintf("%d bytes de audio", len(audio)))
	} else {
		skip("Notas de voz", "sin TTS_BASE_URL")
	}

	if cfg.GmailEnabled() {
		_, err := google.NewAuth(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRefreshToken).Token(ctx)
		report("Google OAuth", err, "refresh token válido")
	} else {
		skip("Google", "sin configurar")
	}

	if cfg.MPAccessToken != "" {
		nick, err := mercadopago.NewClient(cfg.MPAccessToken).Me(ctx)
		report("Mercado Pago", err, nick)
	} else {
		skip("Mercado Pago", "sin configurar")
	}

	if ok {
		fmt.Println("Todo en orden.")
	}
	return ok
}

// publicCandidate dice si el SDP trae una IP pública (srflx por STUN, o host con IP pública):
// sin eso, Meta y el modelo no pueden mandarle audio al worker.
func publicCandidate(sdp string) bool {
	for _, line := range strings.Split(sdp, "\n") {
		if !strings.HasPrefix(line, "a=candidate:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 8 || !strings.EqualFold(f[2], "udp") {
			continue
		}
		ip := net.ParseIP(f[4])
		if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		return true
	}
	return false
}
