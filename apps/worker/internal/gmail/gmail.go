// Package gmail usa la API REST de Gmail con el refresh token del dueño.
// Mensajes nuevos por notificaciones push (Pub/Sub), nunca por polling.
package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/google"
	"github.com/nawesan12/mano/worker/internal/store"
)

const apiBase = "https://gmail.googleapis.com/gmail/v1/users/me/"

type Client struct {
	auth  *google.Auth
	topic string
	hc    *http.Client

	mu          sync.Mutex
	lastHistory uint64
	labelID     string // etiqueta Mano/Archivado

	base string // configurable para tests
}

// New usa el acceso de Google compartido (el mismo que Calendar: una sola renovación del token).
func New(auth *google.Auth, topic string) *Client {
	return &Client{auth: auth, topic: topic, hc: &http.Client{Timeout: 30 * time.Second}, base: apiBase}
}

var errNotFound = errors.New("no encontrado")

func (c *Client) api(ctx context.Context, method, path string, q url.Values, body, out any) error {
	tok, err := c.auth.Token(ctx)
	if err != nil {
		return err
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, u, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("gmail %s %s: %d %s", method, path, res.StatusCode, core.Truncate(string(raw), 300))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Watch registra las notificaciones push. Gmail lo corta a los 7 días: renovar a diario.
func (c *Client) Watch(ctx context.Context) (historyID string, expiration time.Time, err error) {
	if c.topic == "" {
		return "", time.Time{}, errors.New("falta GMAIL_PUBSUB_TOPIC")
	}
	var out struct {
		HistoryID  string `json:"historyId"`
		Expiration string `json:"expiration"`
	}
	if err = c.api(ctx, http.MethodPost, "watch", nil, map[string]any{"topicName": c.topic, "labelIds": []string{"INBOX"}}, &out); err != nil {
		return
	}
	if h, e := strconv.ParseUint(out.HistoryID, 10, 64); e == nil {
		c.mu.Lock()
		if c.lastHistory == 0 {
			c.lastHistory = h
		}
		c.mu.Unlock()
	}
	ms, _ := strconv.ParseInt(out.Expiration, 10, 64)
	return out.HistoryID, time.UnixMilli(ms), nil
}

// NewMessagesSince devuelve los ids nuevos en INBOX desde el último historyId procesado.
func (c *Client) NewMessagesSince(ctx context.Context, pushedHistory uint64) ([]string, error) {
	c.mu.Lock()
	start := c.lastHistory
	c.mu.Unlock()
	if start == 0 || start > pushedHistory {
		start = pushedHistory - 1
	}
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	page := ""
	for {
		q := url.Values{"startHistoryId": {strconv.FormatUint(start, 10)}, "historyTypes": {"messageAdded"}, "labelId": {"INBOX"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		var out struct {
			History []struct {
				MessagesAdded []struct {
					Message struct {
						ID string `json:"id"`
					} `json:"message"`
				} `json:"messagesAdded"`
			} `json:"history"`
			NextPageToken string `json:"nextPageToken"`
		}
		err := c.api(ctx, http.MethodGet, "history", q, nil, &out)
		if errors.Is(err, errNotFound) {
			// historyId vencido: tomo los no leídos del último día para no perder nada
			var l struct {
				Messages []struct {
					ID string `json:"id"`
				} `json:"messages"`
			}
			if err := c.api(ctx, http.MethodGet, "messages", url.Values{"q": {"in:inbox is:unread newer_than:1d"}, "maxResults": {"20"}}, nil, &l); err != nil {
				return nil, err
			}
			for _, m := range l.Messages {
				add(m.ID)
			}
			break
		}
		if err != nil {
			return nil, err
		}
		for _, h := range out.History {
			for _, m := range h.MessagesAdded {
				add(m.Message.ID)
			}
		}
		if page = out.NextPageToken; page == "" {
			break
		}
	}
	c.mu.Lock()
	if pushedHistory > c.lastHistory {
		c.lastHistory = pushedHistory
	}
	c.mu.Unlock()
	return ids, nil
}

type Part struct {
	MimeType string `json:"mimeType"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Data string `json:"data"`
	} `json:"body"`
	Parts []Part `json:"parts"`
}

func (p *Part) header(name string) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func (c *Client) ToEvent(ctx context.Context, id, tenantID string) (core.Event, error) {
	var m struct {
		ID           string `json:"id"`
		ThreadID     string `json:"threadId"`
		Snippet      string `json:"snippet"`
		InternalDate string `json:"internalDate"`
		Payload      Part   `json:"payload"`
	}
	if err := c.api(ctx, http.MethodGet, "messages/"+url.PathEscape(id), url.Values{"format": {"full"}}, nil, &m); err != nil {
		return core.Event{}, err
	}
	body := ExtractText(&m.Payload)
	if body == "" {
		body = m.Snippet
	}
	received := time.Now()
	if ms, err := strconv.ParseInt(m.InternalDate, 10, 64); err == nil {
		received = time.UnixMilli(ms)
	}
	from := m.Payload.header("From")
	fromAddr := core.MailAddress(from)
	replyTo := core.MailAddress(m.Payload.header("Reply-To"))
	if replyTo == "" {
		replyTo = fromAddr
	}
	return core.Event{
		TenantID: tenantID, Source: "gmail", ExternalID: id, Kind: "email",
		Sender: from, Subject: m.Payload.header("Subject"), Body: body, ReceivedAt: received,
		Payload: map[string]any{"threadId": m.ThreadID, "messageId": m.Payload.header("Message-ID"), "replyTo": replyTo, "from": fromAddr},
	}, nil
}

// headerSafe saca saltos de línea: un encabezado de un tercero no puede agregar otros (Bcc, etc.).
func headerSafe(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }

// Reply responde en el mismo hilo. Solo se llama después de una aprobación.
func (c *Client) Reply(ctx context.Context, a store.Action) error {
	to := core.MailAddress(a.To)
	if to == "" {
		return fmt.Errorf("el destinatario %q no es una dirección válida", a.To)
	}
	subject := a.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	raw := strings.Join([]string{
		"To: " + to,
		"Subject: =?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?=",
		"In-Reply-To: " + headerSafe(a.MessageID),
		"References: " + headerSafe(a.MessageID),
		`Content-Type: text/plain; charset="UTF-8"`,
		"",
		a.Body,
	}, "\r\n")
	return c.api(ctx, http.MethodPost, "messages/send", nil, map[string]any{"threadId": a.ThreadID, "raw": base64.URLEncoding.EncodeToString([]byte(raw))}, nil)
}

func decode(d string) string {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(d, "="))
	if err != nil {
		return ""
	}
	return string(b)
}

// ExtractText prefiere text/plain; si solo hay HTML, lo limpia.
func ExtractText(p *Part) string {
	if p == nil {
		return ""
	}
	switch {
	case p.MimeType == "text/plain":
		return decode(p.Body.Data)
	case len(p.Parts) > 0:
		var plain []string
		for i := range p.Parts {
			if p.Parts[i].MimeType == "text/plain" || strings.HasPrefix(p.Parts[i].MimeType, "multipart/") {
				if t := ExtractText(&p.Parts[i]); t != "" && p.Parts[i].MimeType == "text/plain" {
					plain = append(plain, t)
				}
			}
		}
		if s := strings.TrimSpace(strings.Join(plain, "\n")); s != "" {
			return s
		}
		var any []string
		for i := range p.Parts {
			if t := ExtractText(&p.Parts[i]); t != "" {
				any = append(any, t)
			}
		}
		return strings.TrimSpace(strings.Join(any, "\n"))
	case p.MimeType == "text/html":
		return StripHTML(decode(p.Body.Data))
	}
	return ""
}

var (
	reStyle  = regexp.MustCompile(`(?is)<(style|script)[^>]*>.*?</(style|script)>`)
	reBreak  = regexp.MustCompile(`(?i)<br\s*/?>|</p>`)
	reTag    = regexp.MustCompile(`<[^>]+>`)
	reBlanks = regexp.MustCompile(`[ \t]+`)
	reLines  = regexp.MustCompile(`\n\s*\n+`)
	reAngle  = regexp.MustCompile(`<([^>]+)>`)
)

func StripHTML(h string) string {
	h = reStyle.ReplaceAllString(h, " ")
	h = reBreak.ReplaceAllString(h, "\n")
	h = reTag.ReplaceAllString(h, " ")
	h = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`).Replace(h)
	h = reBlanks.ReplaceAllString(h, " ")
	h = reLines.ReplaceAllString(h, "\n")
	lines := strings.Split(h, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// EmailOf: "Marta Pérez <marta@x.com>" → "marta@x.com"
func EmailOf(s string) string {
	if m := reAngle.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return strings.TrimSpace(s)
}

// ArchiveLabel es la etiqueta que deja Mano en lo que archiva: se encuentra y se recupera fácil.
const ArchiveLabel = "Mano/Archivado"

// archiveLabelID busca (o crea, una vez) la etiqueta de archivado.
func (c *Client) archiveLabelID(ctx context.Context) (string, error) {
	c.mu.Lock()
	id := c.labelID
	c.mu.Unlock()
	if id != "" {
		return id, nil
	}
	var list struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := c.api(ctx, http.MethodGet, "labels", nil, nil, &list); err != nil {
		return "", err
	}
	for _, l := range list.Labels {
		if l.Name == ArchiveLabel {
			id = l.ID
		}
	}
	if id == "" {
		var created struct {
			ID string `json:"id"`
		}
		if err := c.api(ctx, http.MethodPost, "labels", nil, map[string]string{"name": ArchiveLabel, "labelListVisibility": "labelShow", "messageListVisibility": "show"}, &created); err != nil {
			return "", err
		}
		id = created.ID
	}
	c.mu.Lock()
	c.labelID = id
	c.mu.Unlock()
	return id, nil
}

func (c *Client) modify(ctx context.Context, id string, add, remove []string) error {
	return c.api(ctx, http.MethodPost, "messages/"+url.PathEscape(id)+"/modify", nil, map[string][]string{"addLabelIds": add, "removeLabelIds": remove}, nil)
}

// Archive saca el mail del inbox y le pone la etiqueta de Mano. No borra nada.
func (c *Client) Archive(ctx context.Context, id string) error {
	label, err := c.archiveLabelID(ctx)
	if err != nil {
		return err
	}
	return c.modify(ctx, id, []string{label}, []string{"INBOX"})
}

// Unarchive lo devuelve al inbox.
func (c *Client) Unarchive(ctx context.Context, id string) error {
	label, err := c.archiveLabelID(ctx)
	if err != nil {
		return err
	}
	return c.modify(ctx, id, []string{"INBOX"}, []string{label})
}
