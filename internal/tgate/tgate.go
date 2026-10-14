// Code copied from mikan (tgate) by tools/extract: the declarations the bot needs.

package tgate

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"subsbot/internal/textutil"
	"sync"
	"time"
)

// DefaultAPI is Telegram's Bot API; tests point the bot at a fake one.
const DefaultAPI = "https://api.telegram.org"

// Client calls the Bot API with one token.
type Client struct {
	base  string
	token string
	hc    *http.Client
	// How long a call may take: a long poll holds its request for pollTimeout and gets
	// this much on top, any other call only this much, an answer to a pre-checkout query
	// preCheckoutTimeout, which Telegram's ten seconds leave it.
	callTimeout time.Duration
	// plus: what dresses a call's body (plus.go), and who hears of a dressed body
	// Telegram refused.
	decorate func(method string, body []byte) ([]byte, bool)
	refused  func(method string, err error)
}

// callTimeout is a call's time, getUpdates apart. Telegram answers in a fraction of it;
// past it the way there is down, and a call that hangs holds a worker of the outbox.
const callTimeout = 15 * time.Second

// preCheckoutTimeout: Telegram cancels the payment of a query not answered within ten
// seconds, so the answer is not worth waiting for past eight.
const preCheckoutTimeout = 8 * time.Second

// NewClient: rt is the way to Telegram (see route.go); nil goes straight.
func NewClient(base, token string, rt http.RoundTripper) *Client {
	if base == "" {
		base = DefaultAPI
	}
	// No timeout on the client as a whole: each call sets its own (see call).
	return &Client{base: strings.TrimRight(base, "/"), token: token, hc: &http.Client{Transport: rt}, callTimeout: callTimeout}
}

// APIError is Telegram's refusal. Code 403 means the user blocked the bot; 429 carries
// RetryAfter.
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string { return fmt.Sprintf("telegram %d: %s", e.Code, e.Description) }

// ErrUnreachable: the Bot API did not answer.
var ErrUnreachable = errors.New("telegram unreachable")

func (c *Client) call(parent context.Context, method string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	// plus: the look (icons, colours, custom emoji) goes on what is sent; a body Telegram
	// refuses with it goes again as it was, so a wrong icon never costs a message.
	if c.decorate != nil {
		if dressed, ok := c.decorate(method, body); ok {
			err := c.post(parent, method, dressed, out)
			if !Refused(err) {
				return err
			}
			if c.refused != nil {
				c.refused(method, err)
			}
		}
	}
	err = c.post(parent, method, body, out)
	// plus: the admin's own icons ride in the body itself; refused, they go too.
	if Refused(err) {
		if plain, ok := stripLook(body); ok {
			if c.refused != nil {
				c.refused(method, err)
			}
			return c.post(parent, method, plain, out)
		}
	}
	return err
}

// post sends a JSON body to method.
func (c *Client) post(parent context.Context, method string, body []byte, out any) error {
	timeout := c.callTimeout
	switch method {
	case "getUpdates":
		timeout += pollTimeout
	case "answerPreCheckoutQuery":
		timeout = min(timeout, preCheckoutTimeout)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p := priorityOf(parent); p != "" { // plus: the lane of the panel's outbox
		req.Header.Set("X-Mikan-Priority", p)
	}
	return c.do(parent, req, out)
}

// unreachable is the error of a request that got no answer: the caller's own when its
// context ended, else ErrUnreachable. The error text would carry the URL, and the URL the
// token: only the cause is kept.
func unreachable(parent context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("%w: %v", ErrUnreachable, err)
}

// do sends a request made for the Bot API and reads its answer into out.
func (c *Client) do(parent context.Context, req *http.Request, out any) error {
	resp, err := c.hc.Do(req)
	if err != nil {
		return unreachable(parent, err)
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("%w: HTTP %d is not a Bot API answer", ErrUnreachable, resp.StatusCode)
	}
	if !r.OK {
		return &APIError{Code: r.ErrorCode, Description: r.Description, RetryAfter: time.Duration(r.Parameters.RetryAfter) * time.Second}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// documentTimeout: a file goes up in one request, which over a slow way to Telegram (a
// node, a proxy) takes longer than a message.
const documentTimeout = 2 * time.Minute

// MaxDocument is the largest file a bot may send.
const MaxDocument = 50 << 20

// SendDocument sends size bytes from file to a chat as a document named name, with a
// caption under it. The body is read from file as it goes up, with its exact length: the
// file is never held in memory.
func (c *Client) SendDocument(parent context.Context, chat int64, name string, file io.Reader, size int64, caption string) (Message, error) {
	if size > MaxDocument {
		return Message{}, fmt.Errorf("a document of %d bytes is over the %d a bot may send", size, MaxDocument)
	}
	// The parts before the file and the closing boundary after it, written by the same
	// multipart writer so they fit together.
	var head bytes.Buffer
	w := multipart.NewWriter(&head)
	_ = w.WriteField("chat_id", strconv.FormatInt(chat, 10))
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	if _, err := w.CreateFormFile("document", name); err != nil {
		return Message{}, err
	}
	prefix := append([]byte(nil), head.Bytes()...)
	head.Reset()
	if err := w.Close(); err != nil {
		return Message{}, err
	}
	trailer := head.Bytes()
	ctx, cancel := context.WithTimeout(parent, documentTimeout)
	defer cancel()
	body := io.MultiReader(bytes.NewReader(prefix), io.LimitReader(file, size), bytes.NewReader(trailer))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/sendDocument", body)
	if err != nil {
		return Message{}, err
	}
	req.ContentLength = int64(len(prefix)) + size + int64(len(trailer))
	req.Header.Set("Content-Type", w.FormDataContentType())
	var m Message
	return m, c.do(parent, req, &m)
}

// User is a Telegram account.
type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
	// SuccessfulPayment: a Stars invoice was paid (a service message from Telegram).
	SuccessfulPayment *SuccessfulPayment `json:"successful_payment"`
	// RefundedPayment: a Stars payment was refunded (a service message from Telegram).
	RefundedPayment *RefundedPayment `json:"refunded_payment"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID         int64             `json:"update_id"`
	Message          *Message          `json:"message"`
	CallbackQuery    *CallbackQuery    `json:"callback_query"`
	PreCheckoutQuery *PreCheckoutQuery `json:"pre_checkout_query"`
	// Raw is the update as Telegram sent it: a bot addon gets it whole, with what these
	// types leave out.
	Raw json.RawMessage `json:"-"`
}

func (u *Update) UnmarshalJSON(b []byte) error {
	type plain Update
	if err := json.Unmarshal(b, (*plain)(u)); err != nil {
		return err
	}
	u.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// Button is an inline keyboard button: one of callback data, a link or the Mini App.
type Button struct {
	Text         string  `json:"text"`
	CallbackData string  `json:"callback_data,omitempty"`
	URL          string  `json:"url,omitempty"`
	WebApp       *WebApp `json:"web_app,omitempty"`
	// plus: a custom emoji before the label and a colour (Bot API 9.4), set by the admin
	// on a button of their own; Telegram refusing them sends the button without (call).
	Icon  string `json:"icon_custom_emoji_id,omitempty"`
	Style string `json:"style,omitempty"`
}

type WebApp struct {
	URL string `json:"url"`
}

type Keyboard struct {
	InlineKeyboard [][]Button `json:"inline_keyboard"`
}

// Me is getMe: the token's bot.
func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, "getMe", struct{}{}, &u)
	return u, err
}

// ChatMember is getChatMember's answer: an account's place in a chat.
type ChatMember struct {
	// Status is creator, administrator, member, restricted, left or kicked.
	Status string `json:"status"`
	// IsMember: a restricted account is still in the chat.
	IsMember bool `json:"is_member"`
}

// In: the account is in the chat, whatever its rights there.
func (m ChatMember) In() bool {
	switch m.Status {
	case "creator", "administrator", "member":
		return true
	case "restricted":
		return m.IsMember
	}
	return false
}

// ChatMember is getChatMember: where user is in chat, a numeric id or @username. In a
// channel only its admins may ask.
func (c *Client) ChatMember(ctx context.Context, chat string, user int64) (ChatMember, error) {
	var m ChatMember
	err := c.call(ctx, "getChatMember", map[string]any{"chat_id": chat, "user_id": user}, &m)
	return m, err
}

// pollTimeout: how long getUpdates waits for news.
const pollTimeout = 50 * time.Second

func (c *Client) Updates(ctx context.Context, offset int64) ([]Update, error) {
	var ups []Update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": int(pollTimeout / time.Second),
		"allowed_updates": []string{"message", "callback_query", "pre_checkout_query"}}, &ups)
	return ups, err
}

// Send posts an HTML message; kb may be nil. A silent one arrives without a sound.
func (c *Client) Send(ctx context.Context, chat int64, text string, kb *Keyboard, silent bool) (Message, error) {
	var m Message
	in := htmlText(chat, text)
	if silent {
		in["disable_notification"] = true
	}
	if kb != nil {
		in["reply_markup"] = kb
	}
	err := c.call(ctx, "sendMessage", in, &m)
	if unparsable(err) {
		asPlain(in, text)
		err = c.call(ctx, "sendMessage", in, &m)
	}
	return m, err
}

// unparsable: Telegram refused the message's HTML. The admin's texts are made to parse
// (markdown.go); should one still not, it goes as plain text instead of not at all.
func unparsable(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == 400 && strings.Contains(strings.ToLower(ae.Description), "can't parse entities")
}

// asPlain makes a request's HTML text plain.
func asPlain(in map[string]any, text string) {
	in["text"] = PlainText(text)
	delete(in, "parse_mode")
}

// PlainText is what an HTML message reads as text: for the rare text Telegram still
// refuses, sent again without formatting rather than not at all.
func PlainText(s string) string {
	var out strings.Builder
	for {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			break
		}
		out.WriteString(s[:i])
		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			s = s[i+1:]
			continue
		}
		s = s[i+j+1:]
	}
	out.WriteString(s)
	return html.UnescapeString(out.String())
}

// SendTo is Send for a Telegram channel target: its numeric chat id or @username.
func (c *Client) SendTo(ctx context.Context, chat string, text string, silent bool) (Message, error) {
	var m Message
	in := htmlText(chat, text)
	if silent {
		in["disable_notification"] = true
	}
	err := c.call(ctx, "sendMessage", in, &m)
	return m, err
}

// EditTo edits a message in a channel identified by numeric id or @username.
func (c *Client) EditTo(ctx context.Context, chat string, msg int64, text string) error {
	in := htmlText(chat, text)
	in["message_id"] = msg
	return notModified(c.call(ctx, "editMessageText", in, nil))
}

// PinTo pins a channel message without notifying subscribers.
func (c *Client) PinTo(ctx context.Context, chat string, msg int64) error {
	return c.call(ctx, "pinChatMessage", map[string]any{"chat_id": chat, "message_id": msg, "disable_notification": true}, nil)
}

// Edit replaces a message's text and buttons. An edit to the same content is not an error.
func (c *Client) Edit(ctx context.Context, chat, msg int64, text string, kb *Keyboard) error {
	in := htmlText(chat, text)
	in["message_id"] = msg
	if kb != nil {
		in["reply_markup"] = kb
	}
	err := c.call(ctx, "editMessageText", in, nil)
	if unparsable(err) {
		asPlain(in, text)
		err = c.call(ctx, "editMessageText", in, nil)
	}
	return notModified(err)
}

// notModified: an edit to the same content is no error.
func notModified(err error) error {
	var ae *APIError
	if errors.As(err, &ae) && strings.Contains(ae.Description, "message is not modified") {
		return nil
	}
	return err
}

// htmlText is the start of a request that sends or edits an HTML text in chat (a numeric id
// or a channel's @username), with no link preview.
func htmlText(chat any, text string) map[string]any {
	return map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
}

func (c *Client) Delete(ctx context.Context, chat, msg int64) error {
	return c.call(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": msg}, nil)
}

// Answer stops the spinner on a pressed button; text, when set, pops up briefly.
func (c *Client) Answer(ctx context.Context, id, text string) error {
	in := map[string]any{"callback_query_id": id}
	if text != "" {
		in["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", in, nil)
}

// DropWebhook switches the bot to polling: a webhook left by another service would make
// every getUpdates fail.
func (c *Client) DropWebhook(ctx context.Context) error {
	return c.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}

// SetCommands shows /start in the command menu.
func (c *Client) SetCommands(ctx context.Context, desc string) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": []map[string]string{{"command": "start", "description": desc}}}, nil)
}

// SetMenuButton puts the Mini App next to the input field of every private chat (url
// empty: back to the command list).
func (c *Client) SetMenuButton(ctx context.Context, text, url string) error {
	btn := map[string]any{"type": "commands"}
	if url != "" {
		btn = map[string]any{"type": "web_app", "text": text, "web_app": map[string]string{"url": url}}
	}
	return c.call(ctx, "setChatMenuButton", map[string]any{"menu_button": btn}, nil)
}

// SuccessfulPayment is what Telegram reports after a Stars payment.
type SuccessfulPayment struct {
	Currency       string `json:"currency"`
	TotalAmount    int64  `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
	ChargeID       string `json:"telegram_payment_charge_id"`
}

// RefundedPayment is what Telegram reports after a Stars payment is refunded, by the bot
// or by Telegram itself.
type RefundedPayment struct {
	Currency       string `json:"currency"`
	TotalAmount    int64  `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
	ChargeID       string `json:"telegram_payment_charge_id"`
}

// PreCheckoutQuery asks the bot to confirm a payment within ten seconds.
type PreCheckoutQuery struct {
	ID             string `json:"id"`
	From           User   `json:"from"`
	Currency       string `json:"currency"`
	TotalAmount    int64  `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
}

// InvoiceLink makes a link to pay in Telegram Stars (XTR, no provider token).
func (c *Client) InvoiceLink(ctx context.Context, title, description, payload string, stars int64) (string, error) {
	var link string
	err := c.call(ctx, "createInvoiceLink", map[string]any{"title": textutil.ClipRunes(title, 32), "description": textutil.ClipRunes(description, 255), "payload": payload,
		"currency": "XTR", "prices": []map[string]any{{"label": textutil.ClipRunes(title, 32), "amount": stars}}}, &link)
	return link, err
}

// AnswerPreCheckout lets the payment go ahead, or refuses it with a reason the buyer sees.
func (c *Client) AnswerPreCheckout(ctx context.Context, id string, ok bool, reason string) error {
	in := map[string]any{"pre_checkout_query_id": id, "ok": ok}
	if !ok {
		in["error_message"] = reason
	}
	return c.call(ctx, "answerPreCheckoutQuery", in, nil)
}

// RefundStars returns a Stars payment.
func (c *Client) RefundStars(ctx context.Context, user int64, chargeID string) error {
	err := c.call(ctx, "refundStarPayment", map[string]any{"user_id": user, "telegram_payment_charge_id": chargeID}, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && strings.Contains(strings.ToUpper(apiErr.Description), "CHARGE_ALREADY_REFUNDED") {
		return nil
	}
	return err
}

// Forward makes a call of the Bot API as an addon put it (the Bot API proxy): the body as it
// came, with its content type, and Telegram's answer as it is. Only a way to Telegram that
// fails is an error; a refusal comes back in the answer, and as an *APIError when it is a
// 429, so the outbox waits it out. The token never leaves this client.
func (c *Client) Forward(parent context.Context, method, contentType string, body []byte) (int, []byte, error) {
	timeout := c.callTimeout
	if strings.HasPrefix(contentType, "multipart/") {
		timeout = documentTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, unreachable(parent, err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		var r struct {
			Description string `json:"description"`
			Parameters  struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		_ = json.Unmarshal(answer, &r)
		return resp.StatusCode, answer, &APIError{Code: 429, Description: r.Description, RetryAfter: time.Duration(r.Parameters.RetryAfter) * time.Second}
	}
	return resp.StatusCode, answer, nil
}

// maxAnswer bounds an answer of the Bot API read for an addon.
const maxAnswer = 4 << 20

var ErrLinkInvalid = errors.New("link_invalid")

var ErrLinkExpired = errors.New("link_expired")

// Channel is the bot's channel and whether joining it is required. The bot checks members
// with getChatMember, which in a channel only its admins may call.
type Channel struct {
	Required bool   `json:"required" doc:"Обязательная подписка: без неё бот показывает вместо меню экран с кнопкой канала"`
	Chat     string `json:"chat" maxLength:"64" doc:"Публичный @username канала или его числовой ID (-100…); бот должен быть администратором канала"`
	URL      string `json:"url,omitempty" maxLength:"256" doc:"Ссылка, по которой вступают (https://t.me/…); пусто: t.me/<username>. Для канала по числовому ID обязательна"`
}

// Link is where a subscriber joins the channel: the admin's link, or the public channel's
// own; "" when there is neither.
func (ch Channel) Link() string {
	if ch.URL != "" {
		return ch.URL
	}
	if name, ok := strings.CutPrefix(ch.Chat, "@"); ok {
		return "https://t.me/" + name
	}
	return ""
}

// Gate: the channel is required and set up. A switch without a channel locks nobody out.
func (ch Channel) Gate() bool { return ch.Required && ch.Chat != "" }

// channelChat is a channel as Bot API methods take it: a public @username (5 to 32 letters,
// digits and underscores, as Telegram allows) or a numeric chat id.
var channelChat = regexp.MustCompile(`^(@[A-Za-z0-9_]{5,32}|-?[0-9]{1,20})$`)

var ErrChannel = errors.New("tg_channel")

var ErrChannelURL = errors.New("tg_channel_url")

// Validate trims the channel and checks it can be asked about and joined: a channel by
// its numeric id has no public link, so the admin's link is required for it.
func (ch *Channel) Validate() error {
	ch.Chat, ch.URL = strings.TrimSpace(ch.Chat), strings.TrimSpace(ch.URL)
	switch {
	case ch.Chat == "" && ch.Required:
		return ErrChannel
	case ch.Chat != "" && !channelChat.MatchString(ch.Chat):
		return ErrChannel
	case ch.URL != "" && !SafeURL(ch.URL):
		return ErrChannelURL
	case ch.Chat != "" && ch.Link() == "":
		return ErrChannelURL
	}
	return nil
}

// SafeURL: links the bot may show — web pages and Telegram links.
func SafeURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" && u.Host != "" || u.Scheme == "tg" && u.Host != "") && u.User == nil
}

// MaxLinks: subscriptions one Telegram account may hold.
const MaxLinks = 5

// ErrOff: the bot is off, or not running yet.
var ErrOff = errors.New("bot_off")

type priority int

const (
	prioPress  priority = iota // plus: a tap on a menu: its user is in the chat right now
	prioReply                  // the user is in the chat and waits
	prioNotice                 // a notification about their subscription
	prioBulk                   // a broadcast
	prioTidy                   // plus: a message removed to keep the chat tidy: no one waits for it
	prioCount
)

// waits: someone is in the chat for the job (plus: a tap or a reply).
func (p priority) waits() bool { return p <= prioReply }

// Limits of the outbox; tests make them faster.
type Limits struct {
	Total    float64 // messages a second in all
	Bulk     float64 // notices and broadcasts, a part of Total
	PerChat  float64 // messages a second in one chat
	Burst    float64 // a chat's short burst: a message, its old menu gone, a new one sent
	Workers  int     // requests in flight
	MaxTries int     // a message that keeps failing is given up after this many sends
}

// DefaultLimits keep under Telegram's: 28 of ~30 a second, broadcasts at 20 so replies
// always have room, one a second in a chat after a burst of three.
var DefaultLimits = Limits{Total: 28, Bulk: 20, PerChat: 1, Burst: 3, Workers: 4, MaxTries: 5}

// job is one piece of work for one chat: a send, an edit, or a few calls in order.
type job struct {
	chat int64
	prio priority
	key  string // a newer job with the same key replaces this one while it waits
	cost float64
	do   func(ctx context.Context, c *Client) error
	done func(error) // nil or called once: sent, given up, or dropped at stop

	tries     int
	notBefore time.Time
}

// bucket is a token bucket: rate tokens a second, at most burst stored.
type bucket struct {
	rate, burst, tokens float64
	last                time.Time
}

func newBucket(rate, burst float64, now time.Time) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: now}
}

func (b *bucket) refill(now time.Time) {
	if now.After(b.last) {
		b.tokens = min(b.burst, b.tokens+b.rate*now.Sub(b.last).Seconds())
		b.last = now
	}
}

// wait is how long until n tokens are there (0: now).
func (b *bucket) wait(now time.Time, n float64) time.Duration {
	b.refill(now)
	if b.tokens >= n {
		return 0
	}
	return time.Duration((n - b.tokens) / b.rate * float64(time.Second))
}

func (b *bucket) take(n float64) { b.tokens -= n }

// Outbox runs the sends of one bot token.
type Outbox struct {
	c    *Client
	lim  Limits
	now  func() time.Time
	log  func(error)
	gone func(chat int64) // the user blocked the bot

	mu       sync.Mutex
	queues   [prioCount]*list.List // of *job, the first to go at the front
	byKey    map[string]*job
	chats    map[int64]*bucket
	busy     map[int64]bool // a job of the chat is being sent
	total    *bucket
	bulk     *bucket
	paused   time.Time           // a 429 without a chat in it stops everything until then
	chatOff  map[int64]time.Time // a 429 on a reply stops only that chat
	wake     chan struct{}
	stopped  chan struct{} // closed when Run ends
	stopped1 sync.Once
}

func NewOutbox(c *Client, lim Limits, now func() time.Time, gone func(int64), logErr func(error)) *Outbox {
	t := now()
	o := &Outbox{c: c, lim: lim, now: now, log: logErr, gone: gone, byKey: map[string]*job{}, chats: map[int64]*bucket{},
		busy: map[int64]bool{}, total: newBucket(lim.Total, lim.Total, t), bulk: newBucket(lim.Bulk, lim.Bulk, t),
		chatOff: map[int64]time.Time{}, wake: make(chan struct{}, 1), stopped: make(chan struct{})}
	for p := range o.queues {
		o.queues[p] = list.New()
	}
	return o
}

func (o *Outbox) poke() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// add queues a job; with a key it replaces a waiting job with the same key in place.
func (o *Outbox) add(j *job) {
	if j.cost == 0 {
		j.cost = 1
	}
	o.mu.Lock()
	if j.key != "" {
		if old, ok := o.byKey[j.key]; ok {
			// Keep the older one's place: the chat waited for it already.
			oldDone := old.done
			*old = *j
			o.byKey[j.key] = old
			o.mu.Unlock()
			if oldDone != nil {
				oldDone(nil)
			}
			o.poke()
			return
		}
		o.byKey[j.key] = j
	}
	o.queues[j.prio].PushBack(j)
	o.mu.Unlock()
	o.poke()
}

// Reply sends to a user who waits in the chat. key merges successive screens of one menu.
func (o *Outbox) Reply(chat int64, key string, cost float64, do func(context.Context, *Client) error) {
	o.add(&job{chat: chat, prio: prioReply, key: key, cost: cost, do: do})
}

// Press draws the screen of a tap (plus): ahead of the replies to messages, so that a wave
// of thousands of /start does not hold up the people already in their menu.
func (o *Outbox) Press(chat int64, key string, do func(context.Context, *Client) error) {
	o.add(&job{chat: chat, prio: prioPress, key: key, cost: 1, do: do})
}

// Tidy removes a message when nothing else waits (plus): a reply never queues behind it.
func (o *Outbox) Tidy(chat int64, do func(context.Context, *Client) error) {
	o.add(&job{chat: chat, prio: prioTidy, do: do})
}

// Notice sends a notification.
func (o *Outbox) Notice(chat int64, do func(context.Context, *Client) error, done func(error)) {
	o.add(&job{chat: chat, prio: prioNotice, do: do, done: done})
}

// Bulk sends a broadcast message.
func (o *Outbox) Bulk(chat int64, do func(context.Context, *Client) error, done func(error)) {
	o.add(&job{chat: chat, prio: prioBulk, do: do, done: done})
}

// Pending is how many messages wait of a priority or below it (prioCount: all).
func (o *Outbox) pending(p priority) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for i := p; i < prioTidy; i++ { // plus: a removal is no message
		n += o.queues[i].Len()
	}
	return n
}

// BulkPending is how many broadcast messages wait: a broadcast is fed in as they go.
func (o *Outbox) BulkPending() int { return o.pending(prioBulk) }

// next takes the first job allowed to go now, by priority, and its tokens; otherwise it
// says how long until something could.
func (o *Outbox) next() (*job, time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	wait := time.Second
	if now.Before(o.paused) {
		return nil, o.paused.Sub(now)
	}
	// plus: with the bot's limit spent nothing can go: thousands waiting are not looked
	// over again for every token.
	if w := o.total.wait(now, 1); w > 0 {
		return nil, w
	}
	for p := range prioCount {
		for e := o.queues[p].Front(); e != nil; e = e.Next() {
			j := e.Value.(*job)
			if o.busy[j.chat] || now.Before(j.notBefore) {
				if now.Before(j.notBefore) {
					wait = min(wait, j.notBefore.Sub(now))
				}
				continue
			}
			if off := o.chatOff[j.chat]; now.Before(off) {
				wait = min(wait, off.Sub(now))
				continue
			}
			cb := o.chats[j.chat]
			if cb == nil {
				cb = newBucket(o.lim.PerChat, o.lim.Burst, now)
				o.chats[j.chat] = cb
			}
			w := max(cb.wait(now, j.cost), o.total.wait(now, j.cost))
			if !p.waits() {
				w = max(w, o.bulk.wait(now, j.cost))
			}
			if w > 0 {
				wait = min(wait, w)
				if o.total.wait(now, j.cost) > 0 {
					// plus: no job behind it, in any lane, takes the total's tokens first:
					// a menu (2) waited for minutes behind thousands of deletes (1).
					return nil, max(wait, time.Millisecond)
				}
				continue
			}
			cb.take(j.cost)
			o.total.take(j.cost)
			if !p.waits() {
				o.bulk.take(j.cost)
			}
			o.queues[p].Remove(e)
			if j.key != "" && o.byKey[j.key] == j {
				delete(o.byKey, j.key)
			}
			o.busy[j.chat] = true
			return j, 0
		}
	}
	return nil, max(wait, time.Millisecond)
}

// Run sends until ctx ends; what is still queued then is dropped.
func (o *Outbox) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range max(1, o.lim.Workers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o.work(ctx)
		}()
	}
	wg.Wait()
	o.mu.Lock()
	var left []*job
	for p := range o.queues {
		for e := o.queues[p].Front(); e != nil; e = e.Next() {
			left = append(left, e.Value.(*job))
		}
		o.queues[p].Init()
	}
	o.byKey = map[string]*job{}
	o.mu.Unlock()
	o.stopped1.Do(func() { close(o.stopped) })
	for _, j := range left {
		if j.done != nil {
			j.done(context.Canceled)
		}
	}
}

// Stopped is closed once Run has ended and dropped what was queued.
func (o *Outbox) Stopped() <-chan struct{} { return o.stopped }

func (o *Outbox) work(ctx context.Context) {
	for ctx.Err() == nil {
		j, wait := o.next()
		if j == nil {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-o.wake:
				t.Stop()
			case <-t.C:
			}
			continue
		}
		err := j.do(ctx, o.c)
		o.finish(j, err)
		o.poke() // the chat is free for its next job
	}
}

func (o *Outbox) finish(j *job, err error) {
	var ae *APIError
	now := o.now()
	o.mu.Lock()
	delete(o.busy, j.chat)
	retry := false
	switch {
	case err == nil:
	case errors.As(err, &ae) && ae.Code == 429:
		// Telegram says how long; a reply's flood is the chat's, the rest the bot's.
		after := max(ae.RetryAfter, time.Second)
		if j.prio.waits() || j.prio == prioTidy { // plus: a removal does not stop the bot either

			o.chatOff[j.chat] = now.Add(after)
		} else if now.Add(after).After(o.paused) {
			o.paused = now.Add(after)
		}
		retry = true
	case errors.As(err, &ae):
		// Refused for good: blocked, chat gone, a bad request. Sending again changes nothing.
	case errors.Is(err, context.Canceled):
	default:
		// The network: again in a moment, a few times.
		j.tries++
		j.notBefore = now.Add(time.Duration(j.tries) * 2 * time.Second)
		retry = j.tries < o.lim.MaxTries
	}
	superseded := false
	if retry && j.key != "" {
		// A newer screen for the same message came while this one was out: that one goes.
		if _, taken := o.byKey[j.key]; taken {
			retry, superseded = false, true
		} else {
			o.byKey[j.key] = j
		}
	}
	if retry {
		// Back to the head of its lane: it keeps its turn.
		o.queues[j.prio].PushFront(j)
	}
	for chat, t := range o.chatOff {
		if !now.Before(t) {
			delete(o.chatOff, chat)
		}
	}
	if len(o.chats) > 5000 {
		// Chats idle long enough have full buckets again; forget them.
		for chat, b := range o.chats {
			if b.wait(now, b.burst) == 0 && !o.busy[chat] {
				delete(o.chats, chat)
			}
		}
	}
	o.mu.Unlock()
	if retry {
		return
	}
	if superseded {
		err = nil
	}
	if errors.As(err, &ae) && ae.Code == 403 && o.gone != nil {
		o.gone(j.chat)
	} else if err != nil && o.log != nil && !errors.Is(err, context.Canceled) {
		o.log(err)
	}
	if j.done != nil {
		j.done(err)
	}
}

// Do runs a call of a bot addon in its chat's turn, at the level it asked for ("notice",
// "bulk", else a reply), and calls done once with how it ended: the Bot API proxy waits for
// Telegram's own answer, so nothing is merged by key here.
func (o *Outbox) Do(chat int64, level string, do func(context.Context, *Client) error, done func(error)) {
	prio := prioReply
	switch level {
	case "notice":
		prio = prioNotice
	case "bulk":
		prio = prioBulk
	}
	o.add(&job{chat: chat, prio: prio, do: do, done: done})
}
