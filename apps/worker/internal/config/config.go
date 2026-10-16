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

	CapDayUSD, CapMonthUSD float64

	GoogleClientID, GoogleClientSecret, GoogleRefreshToken string
	GmailTopic, GmailPushToken                             string

	Port     string
	Location *time.Location
	BriefAt  string // "07:00"
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

		Port:    def("PORT", "8787"),
		BriefAt: def("BRIEF_AT", "07:00"),
	}
	if c.TenantID != "" && !uuidRe.MatchString(c.TenantID) {
		missing = append(missing, "MANO_TENANT_ID: tiene que ser un UUID")
	}
	if !hhmmRe.MatchString(c.BriefAt) {
		missing = append(missing, "BRIEF_AT: formato HH:MM")
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
	if c.GmailEnabled() && c.GmailPushToken == "" {
		missing = append(missing, "GMAIL_PUSH_TOKEN: obligatorio si conectás Gmail")
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("falta configuración en .env:\n  · %s", strings.Join(missing, "\n  · "))
	}
	return c, nil
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
