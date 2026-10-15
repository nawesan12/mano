// Package telegram habla con la Bot API por HTTP (long polling) y conecta los comandos con el agente.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/agent"
	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

type Client struct {
	token, base string
	hc          *http.Client
}

func NewClient(token string) *Client {
	return &Client{token: token, base: "https://api.telegram.org", hc: &http.Client{Timeout: 70 * time.Second}}
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	b, _ := json.Marshal(params)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return err
	}
	if !env.OK {
		return fmt.Errorf("telegram %s: %s", method, env.Description)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}
type Keyboard struct {
	Rows [][]Button `json:"inline_keyboard"`
}

func (c *Client) SendMessage(ctx context.Context, chatID, text string, kb *Keyboard) error {
	p := map[string]any{"chat_id": chatID, "text": core.Truncate(text, 4000)}
	if kb != nil {
		p["reply_markup"] = kb
	}
	return c.call(ctx, "sendMessage", p, nil)
}

func (c *Client) typing(ctx context.Context, chatID string) {
	_ = c.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, nil)
}

type Update struct {
	ID       int64    `json:"update_id"`
	Message  *Message `json:"message"`
	Callback *struct {
		ID      string   `json:"id"`
		From    User     `json:"from"`
		Message *Message `json:"message"`
		Data    string   `json:"data"`
	} `json:"callback_query"`
}
type User struct {
	ID int64 `json:"id"`
}
type Message struct {
	ID   int64 `json:"message_id"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	From  User   `json:"from"`
	Text  string `json:"text"`
	Voice *struct {
		FileID string `json:"file_id"`
	} `json:"voice"`
}

// ── Notifier ───────────────────────────────────────────────

type Notifier struct {
	c     *Client
	owner string
}

func NewNotifier(c *Client, owner string) *Notifier { return &Notifier{c: c, owner: owner} }

func (n *Notifier) Send(ctx context.Context, text string) error {
	if n.owner == "" {
		slog.Warn("sin TELEGRAM_OWNER_CHAT_ID; aviso perdido", "text", text)
		return nil
	}
	return n.c.SendMessage(ctx, n.owner, text, nil)
}

var riskLabel = map[string]string{"bajo": "riesgo bajo", "medio": "riesgo medio", "alto": "RIESGO ALTO"}

func (n *Notifier) ApprovalCard(ctx context.Context, a store.Approval) error {
	if n.owner == "" {
		return nil
	}
	kb := &Keyboard{Rows: [][]Button{
		{{Text: "Aprobar", Data: "ap:" + a.ID + ":ok"}, {Text: "Descartar", Data: "ap:" + a.ID + ":no"}},
	}}
	if a.Action.Type == "gmail.reply" {
		kb.Rows = append(kb.Rows, []Button{{Text: "No me avises más de este remitente", Data: "ap:" + a.ID + ":ig"}})
	}
	text := fmt.Sprintf("Necesita tu ok · %s · %s\n%s\n\n%s", a.Area, riskLabel[a.Risk], a.Title, a.Detail)
	return n.c.SendMessage(ctx, n.owner, text, kb)
}

// ── Bot ────────────────────────────────────────────────────

type STT struct{ BaseURL, Key, Model string }

type Bot struct {
	c      *Client
	owner  string
	agent  *agent.Agent
	models *agent.Models
	store  *store.Store
	notify *Notifier
	stt    STT
}

func NewBot(c *Client, owner string, a *agent.Agent, m *agent.Models, s *store.Store, n *Notifier, stt STT) *Bot {
	return &Bot{c: c, owner: owner, agent: a, models: m, store: s, notify: n, stt: stt}
}

// Run hace long polling hasta que se cancele el contexto.
func (b *Bot) Run(ctx context.Context) {
	var offset int64
	for ctx.Err() == nil {
		var ups []Update
		err := b.c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 50, "allowed_updates": []string{"message", "callback_query"}}, &ups)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("telegram getUpdates", "err", err)
				time.Sleep(3 * time.Second)
			}
			continue
		}
		for _, u := range ups {
			offset = u.ID + 1
			b.handle(ctx, u)
		}
	}
}

// ParseCommand: "/recordar@ManoBot Tema: dato" → ("recordar", "Tema: dato")
func ParseCommand(text string) (cmd, args string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, _, _ = strings.Cut(head, "@")
	return strings.ToLower(head), strings.TrimSpace(rest), true
}

var (
	callbackRe = regexp.MustCompile(`^ap:([0-9a-f-]{36}):(ok|no|ig)$`)
	factRe     = regexp.MustCompile(`(?s)^([^:]{1,60}):\s*(.+)$`)
)

func (b *Bot) isOwner(chatID int64) bool {
	return b.owner != "" && strconv.FormatInt(chatID, 10) == b.owner
}

func (b *Bot) handle(ctx context.Context, u Update) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("telegram panic", "err", r)
		}
	}()

	if cb := u.Callback; cb != nil && cb.Message != nil {
		if !b.isOwner(cb.Message.Chat.ID) {
			_ = b.c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": cb.ID, "text": "No autorizado"}, nil)
			return
		}
		m := callbackRe.FindStringSubmatch(cb.Data)
		if m == nil {
			return
		}
		_ = b.c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": cb.ID, "text": "Procesando…"}, nil)
		d := map[string]agent.Decision{"ok": agent.Approve, "no": agent.Reject, "ig": agent.IgnoreSender}[m[2]]
		reply := b.agent.OnDecision(ctx, m[1], d, "telegram:"+strconv.FormatInt(cb.From.ID, 10))
		_ = b.c.call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": cb.Message.Chat.ID, "message_id": cb.Message.ID, "reply_markup": Keyboard{Rows: [][]Button{}}}, nil)
		_ = b.notify.Send(ctx, reply)
		return
	}

	msg := u.Message
	if msg == nil {
		return
	}
	chat := strconv.FormatInt(msg.Chat.ID, 10)
	cmd, args, isCmd := ParseCommand(msg.Text)

	if isCmd && cmd == "start" {
		if b.isOwner(msg.Chat.ID) {
			_ = b.c.SendMessage(ctx, chat, "Hola. Ya estoy trabajando. Escribime o mandame un audio. Comandos: /resumen /pendientes /gasto /recordar /olvidar", nil)
		} else {
			_ = b.c.SendMessage(ctx, chat, "Tu chat id es "+chat+". Ponelo en TELEGRAM_OWNER_CHAT_ID y reiniciá el worker.", nil)
		}
		return
	}
	if !b.isOwner(msg.Chat.ID) {
		return // solo el dueño puede hablarle
	}
	say := func(t string) { _ = b.c.SendMessage(ctx, chat, t, nil) }

	switch {
	case isCmd && cmd == "resumen":
		b.c.typing(ctx, chat)
		text, err := b.agent.Brief(ctx)
		if err != nil {
			text = "No pude armar el resumen: " + err.Error()
		}
		say(text)
	case isCmd && cmd == "pendientes":
		p, err := b.store.PendingApprovals(ctx)
		if err != nil || len(p) == 0 {
			say("No hay nada esperando tu ok.")
			return
		}
		for _, a := range p {
			_ = b.notify.ApprovalCard(ctx, a)
		}
	case isCmd && cmd == "gasto":
		sp, caps, _ := b.models.Status(ctx)
		say(fmt.Sprintf("Hoy USD %.3f de %.2f\nMes USD %.2f de %.2f\nÚltima hora USD %.3f", sp.DayUSD, caps.DayUSD, sp.MonthUSD, caps.MonthUSD, sp.HourUSD))
	case isCmd && cmd == "recordar":
		m := factRe.FindStringSubmatch(args)
		if m == nil {
			say("Usalo así: /recordar Tema: dato. Por ejemplo: /recordar Honorarios: 3% más IVA")
			return
		}
		if err := b.store.AddFact(ctx, strings.TrimSpace(m[1]), strings.TrimSpace(m[2])); err != nil {
			say("No pude guardarlo: " + err.Error())
			return
		}
		say("Anotado: " + strings.TrimSpace(m[1]) + ".")
	case isCmd && cmd == "olvidar":
		if args == "" {
			say("Usalo así: /olvidar Tema")
			return
		}
		n, err := b.store.ForgetTopic(ctx, args)
		switch {
		case err != nil:
			say("No pude borrarlo: " + err.Error())
		case n == 0:
			say("No tenía nada guardado sobre " + args + ".")
		default:
			say(fmt.Sprintf("Olvidé %d dato(s) sobre %s.", n, args))
		}
	case isCmd:
		say("No conozco ese comando. Probá /resumen, /pendientes, /gasto, /recordar u /olvidar.")
	case msg.Voice != nil:
		if b.stt.BaseURL == "" {
			say("Todavía no tengo configurada la transcripción de audios (STT_BASE_URL). Escribime por ahora.")
			return
		}
		b.c.typing(ctx, chat)
		text, err := b.transcribe(ctx, msg.Voice.FileID)
		if err != nil {
			say("No pude entender el audio: " + err.Error())
			return
		}
		say("Entendí: \"" + core.Truncate(text, 300) + "\"")
		say(b.agent.OnOwnerMessage(ctx, text))
	case strings.TrimSpace(msg.Text) != "":
		b.c.typing(ctx, chat)
		say(b.agent.OnOwnerMessage(ctx, msg.Text))
	}
}

func (b *Bot) transcribe(ctx context.Context, fileID string) (string, error) {
	var f struct {
		Path string `json:"file_path"`
	}
	if err := b.c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return "", err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.c.base+"/file/bot"+b.c.token+"/"+f.Path, nil)
	res, err := b.c.hc.Do(req)
	if err != nil {
		return "", err
	}
	audio, _ := io.ReadAll(io.LimitReader(res.Body, 25<<20))
	res.Body.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "nota.ogg")
	_, _ = fw.Write(audio)
	_ = w.WriteField("model", b.stt.Model)
	_ = w.WriteField("language", "es")
	_ = w.Close()

	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(b.stt.BaseURL, "/")+"/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if b.stt.Key != "" {
		req.Header.Set("Authorization", "Bearer "+b.stt.Key)
	}
	res, err = b.c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("transcripción falló: %d", res.StatusCode)
	}
	var out struct {
		Text string `json:"text"`
	}
	return out.Text, json.NewDecoder(res.Body).Decode(&out)
}
