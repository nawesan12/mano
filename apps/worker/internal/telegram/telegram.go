// Package telegram habla con la Bot API por HTTP (long polling) y traduce a owner.Service.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/owner"
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

// ── Canal hacia el dueño ───────────────────────────────────

// Channel implementa owner.Channel y agent.Notifier sobre el chat del dueño.
type Channel struct {
	c     *Client
	owner string
}

func NewChannel(c *Client, ownerChat string) *Channel { return &Channel{c: c, owner: ownerChat} }

func (n *Channel) Ready() bool { return n != nil && n.owner != "" }

func (n *Channel) Send(ctx context.Context, text string) error {
	if !n.Ready() {
		return fmt.Errorf("sin TELEGRAM_OWNER_CHAT_ID")
	}
	return n.c.SendMessage(ctx, n.owner, text, nil)
}

var riskLabel = map[string]string{"bajo": "riesgo bajo", "medio": "riesgo medio", "alto": "RIESGO ALTO"}

func (n *Channel) ApprovalCard(ctx context.Context, a store.Approval) error {
	if !n.Ready() {
		return fmt.Errorf("sin TELEGRAM_OWNER_CHAT_ID")
	}
	kb := &Keyboard{Rows: [][]Button{
		{{Text: "Aprobar", Data: "ap:" + a.ID + ":ok"}, {Text: "Descartar", Data: "ap:" + a.ID + ":no"}},
		{{Text: "No me avises más de este remitente", Data: "ap:" + a.ID + ":ig"}},
	}}
	text := fmt.Sprintf("Necesita tu ok · %s · %s\n%s\n\n%s", a.Area, riskLabel[a.Risk], a.Title, a.Detail)
	return n.c.SendMessage(ctx, n.owner, text, kb)
}

// ── Bot ────────────────────────────────────────────────────

type Bot struct {
	c     *Client
	ch    *Channel
	owner string
	svc   *owner.Service
}

func NewBot(c *Client, ch *Channel, ownerChat string, svc *owner.Service) *Bot {
	return &Bot{c: c, ch: ch, owner: ownerChat, svc: svc}
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
		_ = b.c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": cb.ID, "text": "Procesando…"}, nil)
		if b.svc.HandleButton(ctx, b.ch, cb.Data, "telegram:"+strconv.FormatInt(cb.From.ID, 10)) {
			_ = b.c.call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": cb.Message.Chat.ID, "message_id": cb.Message.ID, "reply_markup": Keyboard{Rows: [][]Button{}}}, nil)
		}
		return
	}

	msg := u.Message
	if msg == nil {
		return
	}
	chat := strconv.FormatInt(msg.Chat.ID, 10)
	if !b.isOwner(msg.Chat.ID) {
		if cmd, _, ok := owner.ParseCommand(msg.Text); ok && cmd == "start" {
			_ = b.c.SendMessage(ctx, chat, "Tu chat id es "+chat+". Ponelo en TELEGRAM_OWNER_CHAT_ID y reiniciá el worker.", nil)
		}
		return // solo el dueño puede hablarle
	}
	b.c.typing(ctx, chat)
	switch {
	case msg.Voice != nil:
		audio, err := b.download(ctx, msg.Voice.FileID)
		if err != nil {
			_ = b.ch.Send(ctx, "No pude bajar el audio: "+err.Error())
			return
		}
		b.svc.HandleAudio(ctx, b.ch, audio, "nota.ogg")
	default:
		b.svc.HandleText(ctx, b.ch, msg.Text)
	}
}

func (b *Bot) download(ctx context.Context, fileID string) ([]byte, error) {
	var f struct {
		Path string `json:"file_path"`
	}
	if err := b.c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.c.base+"/file/bot"+b.c.token+"/"+f.Path, nil)
	res, err := b.c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(io.LimitReader(res.Body, 25<<20))
}
