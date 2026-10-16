// Package config lee el .env de la raíz del monorepo y valida la configuración.
package config

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SupabaseURL, SupabaseKey, TenantID string

	TelegramToken, OwnerChatID string

	WAPhoneID, WAToken, WAAppSecret, WAVerifyToken, WAOwnerPhone string
	WATemplateNotice, WATemplateLang, WAAPIVersion               string
	OwnerChannel                                                 string // telegram | whatsapp

	CheapProvider, CheapBaseURL, CheapKey, CheapModel string
	CheapPriceIn, CheapPriceOut                       float64
	TopProvider, TopBaseURL, TopKey, TopModel         string
	TopPriceIn, TopPriceOut                           float64

	STTBaseURL, STTKey, STTModel string

	// Notas de voz para lo importante (endpoint compatible con OpenAI /audio/speech).
	TTSBaseURL, TTSKey, TTSModel, TTSVoice string
	TTSPricePerMChars                      float64

	// Voz: Mano llama al dueño por WhatsApp y habla con un modelo de voz en tiempo real.
	VoiceEnabled                                bool
	RealtimeBaseURL, RealtimeKey, RealtimeModel string
	RealtimeVoice, RealtimeTranscribe           string
	RealtimePriceIn, RealtimePriceOut           float64
	CallMax, CallRing                           time.Duration
	WACallUSDPerMin                             float64
	WATemplateCallPermission, STUNURL           string

	CapDayUSD, CapMonthUSD float64

	GoogleClientID, GoogleClientSecret, GoogleRefreshToken string
	GmailTopic, GmailPushToken                             string

	MPAccessToken, MPWebhookSecret string

	// Mercado Libre (ventas y envíos): app de developers.mercadolibre.com.ar y refresh token del vendedor.
	MLClientID, MLClientSecret, MLRefreshToken string
	// Transferencias (Payouts): apagadas por defecto; requieren habilitación de Mercado Pago.
	MPPayoutsEnabled               bool
	MPPayoutsKey                   string // PEM PKCS#8 Ed25519 o semilla base64
	MPPayoutsTest                  bool
	MPPayoutMaxARS, MPPayoutDayARS float64

	// Google Calendar: usa el mismo OAuth que Gmail (el refresh token tiene que incluir calendar.readonly).
	CalendarEnabled              bool
	PublicURL, CalendarPushToken string

	// ARCA (facturación electrónica): certificado de la CUIT con el servicio wsfe asociado.
	ArcaEnv, ArcaCertFile, ArcaKeyFile string
	ArcaCUIT                           int64
	ArcaPtoVta, ArcaConcepto           int
	ArcaAuto                           bool
	ArcaCFMax                          float64

	PanelToken string // token compartido con el panel web para mandar decisiones

	Port     string
	Location *time.Location
	BriefAt  string // "07:00"
	CloseAt  string // "19:00"; vacío = sin cierre del día
}

func (c Config) TelegramEnabled() bool { return c.TelegramToken != "" }
func (c Config) WhatsAppEnabled() bool {
	return c.WAPhoneID != "" && c.WAToken != "" && c.WAAppSecret != ""
}

func (c Config) GmailEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != "" && c.GoogleRefreshToken != ""
}

// LoadDotenv carga KEY=VALUE sin pisar variables ya definidas.
func LoadDotenv(paths ...string) {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
			if _, set := os.LookupEnv(k); !set {
				_ = os.Setenv(k, v)
			}
		}
		_ = f.Close()
		return
	}
}

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
	hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
)

// Load valida todo de una vez y devuelve la lista completa de lo que falta.
func Load() (Config, error) {
	var missing []string
	str := func(k string, required bool) string {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" && required {
			missing = append(missing, k+": falta")
		}
		return v
	}
	def := func(k, d string) string {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
		return d
	}
	num := func(k string, d float64, hint string) float64 {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" && d > 0 {
			return d
		}
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n <= 0 {
			missing = append(missing, k+": "+hint)
		}
		return n
	}
	provider := func(k string) string {
		v := str(k, true)
		if v != "" && v != "anthropic" && v != "openai-compatible" {
			missing = append(missing, k+": anthropic u openai-compatible")
		}
		return v
	}

	c := Config{
		SupabaseURL:   strings.TrimRight(str("SUPABASE_URL", true), "/"),
		SupabaseKey:   str("SUPABASE_SERVICE_ROLE_KEY", true),
		TenantID:      str("MANO_TENANT_ID", true),
		TelegramToken: str("TELEGRAM_BOT_TOKEN", false),
		OwnerChatID:   str("TELEGRAM_OWNER_CHAT_ID", false),

		WAPhoneID:        str("WA_PHONE_NUMBER_ID", false),
		WAToken:          str("WA_ACCESS_TOKEN", false),
		WAAppSecret:      str("WA_APP_SECRET", false),
		WAVerifyToken:    str("WA_VERIFY_TOKEN", false),
		WAOwnerPhone:     digits(os.Getenv("WA_OWNER_PHONE")),
		WATemplateNotice: str("WA_TEMPLATE_NOTICE", false),
		WATemplateLang:   def("WA_TEMPLATE_LANG", "es_AR"),
		WAAPIVersion:     def("WA_API_VERSION", "v26.0"),
		OwnerChannel:     def("OWNER_CHANNEL", "telegram"),

		CheapProvider: provider("CHEAP_PROVIDER"),
		CheapBaseURL:  str("CHEAP_BASE_URL", false),
		CheapKey:      str("CHEAP_API_KEY", false),
		CheapModel:    str("CHEAP_MODEL", true),
		CheapPriceIn:  num("CHEAP_PRICE_IN", 0, "USD por millón de tokens de entrada"),
		CheapPriceOut: num("CHEAP_PRICE_OUT", 0, "USD por millón de tokens de salida"),

		TopProvider: provider("TOP_PROVIDER"),
		TopBaseURL:  str("TOP_BASE_URL", false),
		TopKey:      str("TOP_API_KEY", false),
		TopModel:    str("TOP_MODEL", true),
		TopPriceIn:  num("TOP_PRICE_IN", 0, "completalo con la tarifa vigente del proveedor"),
		TopPriceOut: num("TOP_PRICE_OUT", 0, "completalo con la tarifa vigente del proveedor"),

		STTBaseURL: str("STT_BASE_URL", false),
		STTKey:     str("STT_API_KEY", false),
		STTModel:   def("STT_MODEL", "whisper-1"),

		CapDayUSD:   num("CAP_DAY_USD", 3, "número positivo"),
		CapMonthUSD: num("CAP_MONTH_USD", 40, "número positivo"),

		GoogleClientID:     str("GOOGLE_CLIENT_ID", false),
		GoogleClientSecret: str("GOOGLE_CLIENT_SECRET", false),
		GoogleRefreshToken: str("GOOGLE_REFRESH_TOKEN", false),
		GmailTopic:         str("GMAIL_PUBSUB_TOPIC", false),
		GmailPushToken:     str("GMAIL_PUSH_TOKEN", false),

		MLClientID:      str("ML_CLIENT_ID", false),
		MLClientSecret:  str("ML_CLIENT_SECRET", false),
		MLRefreshToken:  str("ML_REFRESH_TOKEN", false),
		MPAccessToken:   str("MP_ACCESS_TOKEN", false),
		MPWebhookSecret: str("MP_WEBHOOK_SECRET", false),

		PanelToken: str("PANEL_TOKEN", false),

		Port:    def("PORT", "8787"),
		BriefAt: def("BRIEF_AT", "07:00"),
		CloseAt: str("BRIEF_CLOSE_AT", false),
	}
	if c.TenantID != "" && !uuidRe.MatchString(c.TenantID) {
		missing = append(missing, "MANO_TENANT_ID: tiene que ser un UUID")
	}
	if !hhmmRe.MatchString(c.BriefAt) {
		missing = append(missing, "BRIEF_AT: formato HH:MM")
	}
	if c.PanelToken != "" && len(c.PanelToken) < 24 {
		missing = append(missing, "PANEL_TOKEN: usá al menos 24 caracteres al azar (openssl rand -hex 24)")
	}
	if c.CloseAt != "" && !hhmmRe.MatchString(c.CloseAt) {
		missing = append(missing, "BRIEF_CLOSE_AT: formato HH:MM, o vacío para no mandar cierre")
	}
	loc, err := time.LoadLocation(def("TZ", "America/Argentina/Buenos_Aires"))
	if err != nil {
		missing = append(missing, "TZ: zona horaria inválida")
		loc = time.UTC
	}
	c.Location = loc
	if !c.TelegramEnabled() && !c.WhatsAppEnabled() {
		missing = append(missing, "canal del dueño: configurá TELEGRAM_BOT_TOKEN o WhatsApp (WA_PHONE_NUMBER_ID, WA_ACCESS_TOKEN, WA_APP_SECRET)")
	}
	if c.WhatsAppEnabled() {
		if c.WAVerifyToken == "" {
			missing = append(missing, "WA_VERIFY_TOKEN: obligatorio para registrar el webhook")
		}
		if c.WAOwnerPhone == "" {
			missing = append(missing, "WA_OWNER_PHONE: tu número con código de país, ej. 5492231234567")
		}
	}
	switch c.OwnerChannel {
	case "telegram":
		if !c.TelegramEnabled() {
			missing = append(missing, "OWNER_CHANNEL=telegram pero falta TELEGRAM_BOT_TOKEN")
		}
	case "whatsapp":
		if !c.WhatsAppEnabled() {
			missing = append(missing, "OWNER_CHANNEL=whatsapp pero falta la configuración de WhatsApp")
		}
	default:
		missing = append(missing, "OWNER_CHANNEL: telegram o whatsapp")
	}
	c.TTSBaseURL = strings.TrimRight(str("TTS_BASE_URL", false), "/")
	if c.TTSBaseURL != "" {
		c.TTSKey = str("TTS_API_KEY", false)
		c.TTSModel = def("TTS_MODEL", "gpt-4o-mini-tts")
		c.TTSVoice = def("TTS_VOICE", "marin")
		c.TTSPricePerMChars = num("TTS_PRICE_PER_M_CHARS", 0, "USD por millón de caracteres (tarifa vigente)")
	}

	c.PublicURL = strings.TrimRight(str("PUBLIC_URL", false), "/")
	c.CalendarEnabled = truthy(os.Getenv("CALENDAR_ENABLED"))
	if c.CalendarEnabled {
		c.CalendarPushToken = str("CALENDAR_PUSH_TOKEN", true)
		if !c.GmailEnabled() {
			missing = append(missing, "CALENDAR_ENABLED: usa el OAuth de Google, configurá GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET y GOOGLE_REFRESH_TOKEN")
		}
		if !strings.HasPrefix(c.PublicURL, "https://") {
			missing = append(missing, "PUBLIC_URL: la URL https pública del worker (Google solo avisa a https con certificado válido)")
		}
	}

	c.VoiceEnabled = truthy(os.Getenv("VOICE_ENABLED"))
	if c.VoiceEnabled {
		c.RealtimeBaseURL = def("REALTIME_BASE_URL", "https://api.openai.com/v1")
		c.RealtimeKey = str("REALTIME_API_KEY", true)
		c.RealtimeModel = def("REALTIME_MODEL", "gpt-realtime-2.1")
		c.RealtimeVoice = def("REALTIME_VOICE", "marin")
		c.RealtimeTranscribe = def("REALTIME_TRANSCRIBE_MODEL", "gpt-4o-mini-transcribe")
		c.RealtimePriceIn = num("REALTIME_PRICE_IN", 0, "USD por millón de tokens de audio de entrada (tarifa vigente)")
		c.RealtimePriceOut = num("REALTIME_PRICE_OUT", 0, "USD por millón de tokens de audio de salida (tarifa vigente)")
		c.CallMax = time.Duration(num("CALL_MAX_SECONDS", 300, "segundos")) * time.Second
		c.CallRing = time.Duration(num("CALL_RING_SECONDS", 45, "segundos")) * time.Second
		c.WACallUSDPerMin = num("WA_CALL_USD_PER_MIN", 0.0101, "USD por minuto de llamada saliente")
		c.WATemplateCallPermission = str("WA_TEMPLATE_CALL_PERMISSION", false)
		c.STUNURL = def("STUN_URL", "stun:stun.l.google.com:19302")
		if !c.WhatsAppEnabled() {
			missing = append(missing, "VOICE_ENABLED: las llamadas usan WhatsApp, configurá WA_PHONE_NUMBER_ID, WA_ACCESS_TOKEN y WA_APP_SECRET")
		}
	}
	if c.MLClientID != "" && (c.MLClientSecret == "" || c.MLRefreshToken == "") {
		missing = append(missing, "Mercado Libre: con ML_CLIENT_ID también van ML_CLIENT_SECRET y ML_REFRESH_TOKEN (scope offline_access)")
	}
	if cuit := digits(os.Getenv("ARCA_CUIT")); cuit != "" {
		c.ArcaCUIT, _ = strconv.ParseInt(cuit, 10, 64)
		c.ArcaEnv = def("ARCA_ENV", "homologacion")
		c.ArcaCertFile = str("ARCA_CERT_FILE", true)
		c.ArcaKeyFile = str("ARCA_KEY_FILE", true)
		c.ArcaPtoVta = int(num("ARCA_PTO_VTA", 0, "punto de venta de tipo Web Services"))
		c.ArcaConcepto = int(num("ARCA_CONCEPTO", 2, "1 productos, 2 servicios, 3 ambos"))
		c.ArcaAuto = os.Getenv("ARCA_AUTO") != "false"
		c.ArcaCFMax = num("ARCA_CF_MAX", 10000000, "monto desde el que hay que identificar al consumidor final")
		if len(cuit) != 11 {
			missing = append(missing, "ARCA_CUIT: 11 dígitos")
		}
		if c.ArcaEnv != "homologacion" && c.ArcaEnv != "produccion" {
			missing = append(missing, "ARCA_ENV: homologacion o produccion")
		}
	}
	c.MPPayoutsEnabled = truthy(os.Getenv("MP_PAYOUTS_ENABLED"))
	if c.MPPayoutsEnabled {
		if c.MPAccessToken == "" {
			missing = append(missing, "MP_PAYOUTS_ENABLED: falta MP_ACCESS_TOKEN")
		}
		c.MPPayoutsTest = truthy(os.Getenv("MP_PAYOUTS_TEST"))
		if path := str("MP_PAYOUTS_KEY_FILE", !c.MPPayoutsTest); path != "" {
			b, err := os.ReadFile(path)
			if err != nil {
				missing = append(missing, "MP_PAYOUTS_KEY_FILE: no pude leer "+path)
			}
			c.MPPayoutsKey = string(b)
		}
		c.MPPayoutMaxARS = num("MP_PAYOUT_MAX_ARS", 500000, "tope por transferencia en pesos")
		c.MPPayoutDayARS = num("MP_PAYOUT_DAY_MAX_ARS", 1000000, "tope diario de transferencias en pesos")
	}
	if c.MPAccessToken != "" && c.MPWebhookSecret == "" {
		missing = append(missing, "MP_WEBHOOK_SECRET: la clave secreta del webhook de Mercado Pago, para validar la firma")
	}
	if c.GmailEnabled() && c.GmailPushToken == "" {
		missing = append(missing, "GMAIL_PUSH_TOKEN: obligatorio si conectás Gmail")
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("falta configuración en .env:\n  · %s", strings.Join(missing, "\n  · "))
	}
	return c, nil
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "si", "sí", "yes", "on":
		return true
	}
	return false
}

// digits deja solo números: "+54 9 223 123-4567" → "5492231234567" (formato wa_id).
func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
