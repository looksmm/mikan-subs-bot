package tgbot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"subsbot/internal/db"
	"subsbot/internal/tgate"
)

// Deps is what the bot's screens need: the panel's data and the way to Telegram.
type Deps struct {
	Backend Backend
	// Client and Outbox send what the bot says; the outbox paces it by Telegram's limits.
	Client *tgate.Client
	Outbox *tgate.Outbox
	// Go runs f as work the bot's caller waits for before it stops; what must not hold up
	// the update loop goes through it.
	Go  func(f func())
	Log *slog.Logger
	Now func() time.Time
}

// Bot is the bot's screens: the menu, the shop, the payment messages and the notices. It
// runs as the bot addon (cmd/mikan-telegram) on the updates and the events of the panel;
// Panel is the panel's side.
type Bot struct {
	d Deps

	mu sync.Mutex
	// Subscriptions somebody asked to take over: by subscription, and who was refused.
	transfers map[int64]transfer
	refused   map[string]time.Time

	// plus: what the bot has on top of mikan's official one (plus_*.go); nil is mikan's bot.
	x *ext
}

func New(d Deps) *Bot {
	return &Bot{d: d, transfers: map[int64]transfer{}, refused: map[string]time.Time{}}
}

func (b *Bot) config(ctx context.Context) Config { return b.d.Backend.Config(ctx) }

// Started sets up what the chats show: the command menu and the Mini App's button beside
// the input field, in the bot's language. An error means some of it is not set: the caller
// tries again.
func (b *Bot) Started(ctx context.Context) error {
	cfg := b.config(ctx)
	w := wordsFor(cfg.Lang)
	text, url := "", b.miniAppURL(ctx, cfg)
	if url != "" {
		text = w.menuApp
	}
	return errors.Join(b.d.Client.SetCommands(ctx, w.commands), b.d.Client.SetMenuButton(ctx, text, url))
}

// Update takes a message or a press in a private chat, the flood guard passed. It is
// answered, or dropped, as it comes: what the screens cannot do is answered in the chat.
func (b *Bot) Update(ctx context.Context, up tgate.Update) {
	b.seen(up) // plus: the bot's list of chats
	switch {
	case up.CallbackQuery != nil && up.CallbackQuery.Message != nil:
		if cmd, rest, _ := strings.Cut(up.CallbackQuery.Data, ":"); cmd == "ta" || cmd == "tx" {
			b.onTransfer(ctx, up.CallbackQuery)
		} else if cmd == freshCode {
			b.onFresh(ctx, up.CallbackQuery, rest) // plus: a button under the admin's message
		} else if cmd == taggedCode {
			b.onTagged(ctx, up.CallbackQuery, rest) // plus: the same, counted
		} else {
			b.onPress(ctx, up.CallbackQuery)
		}
	case up.Message != nil && up.Message.From != nil:
		b.onMessage(ctx, up.Message)
	}
}

var (
	linkInText  = regexp.MustCompile(`https?://\S+`)
	nativeToken = regexp.MustCompile(`^[A-Za-z0-9]{24}$`)
)

// maxLinksRead: how many addresses of one message are looked at, so that a long list is
// not a hundred database lookups.
const maxLinksRead = 3

// onMessage: /start (with a code from a subscription page), a subscription link, or
// anything else — every message brings the main menu back to the bottom of the chat.
func (b *Bot) onMessage(ctx context.Context, m *tgate.Message) {
	chat := m.Chat.ID
	cfg := b.config(ctx)
	w := wordsFor(cfg.Lang)
	if cfg.CleanChat {
		b.tidy(chat, m.MessageID) // plus: was a reply: 11k /start had all their deletes before any menu
	}
	var notice string
	text := strings.TrimSpace(m.Text)
	switch {
	case strings.HasPrefix(text, "/start "):
		notice = b.linkByCode(ctx, w, chat, who(m.From), strings.TrimSpace(strings.TrimPrefix(text, "/start ")))
	default:
		notice = b.linkByAddress(ctx, w, chat, who(m.From), text)
	}
	b.freshMenu(chat, notice)
}

// freshMenu sends the main menu as a new message and removes the previous one, so the
// chat keeps a single menu at the bottom. Several messages in a row get one new menu.
func (b *Bot) freshMenu(chat int64, notice string) {
	// plus: one message (was 2: the old menu's removal is a tidy job of its own now).
	b.d.Outbox.Reply(chat, "menu:"+strconv.FormatInt(chat, 10), 1, func(ctx context.Context, c *tgate.Client) error {
		return b.sendMenu(ctx, c, chat, notice)
	})
}

func (b *Bot) sendMenu(ctx context.Context, c *tgate.Client, chat int64, notice string) error {
	text, kb := b.screen(ctx, b.config(ctx), chat, "m", notice)
	sent, err := c.Send(ctx, chat, text, kb, false)
	if err != nil {
		return err
	}
	if old, err := b.d.Backend.Chat(ctx, chat); err == nil && old.MenuMsgID != 0 && old.MenuMsgID != sent.MessageID {
		b.tidy(chat, old.MenuMsgID)
	}
	_ = b.d.Backend.SetMenu(ctx, chat, sent.MessageID)
	return nil
}

// tidy removes a message once no reply waits (plus). Refused (too old, already gone) is
// no news, as it was not when the removal went with the reply.
func (b *Bot) tidy(chat, msg int64) {
	b.d.Outbox.Tidy(chat, func(ctx context.Context, c *tgate.Client) error {
		err := c.Delete(ctx, chat, msg)
		var ae *tgate.APIError
		if errors.As(err, &ae) && ae.Code != 429 {
			return nil
		}
		return err
	})
}

// answer stops the spinner on a pressed button. A call to Telegram that hangs must not
// hold up the update loop, so it goes on its own. plus: once per press (AnswerAll may have
// answered it already), again once when the way to Telegram failed, and a refusal is logged.
func (b *Bot) answer(ctx context.Context, id string) {
	if !b.firstAnswer(id) {
		return
	}
	b.d.Go(func() { b.answerNow(ctx, id) })
}

// onPress edits the menu message in place: the chat never fills up with menus. The
// spinner on the button stops at once; the screen is drawn when the chat's turn comes,
// and a newer tap on the same menu replaces a screen not yet drawn.
func (b *Bot) onPress(ctx context.Context, q *tgate.CallbackQuery) {
	chat, msg := q.Message.Chat.ID, q.Message.MessageID
	b.answer(ctx, q.ID)
	// What a tap changes happens now, only its screen may wait.
	data, notice := b.act(ctx, chat, q.Data)
	b.d.Outbox.Press(chat, "edit:"+strconv.FormatInt(chat, 10)+":"+strconv.FormatInt(msg, 10), func(ctx context.Context, c *tgate.Client) error {
		text, kb := b.screen(ctx, b.config(ctx), chat, data, notice)
		if err := c.Edit(ctx, chat, msg, text, kb); err != nil {
			var ae *tgate.APIError
			if errors.As(err, &ae) && ae.Code == 429 {
				return err
			}
			// Too old to edit, or gone: a new menu instead.
			return b.sendMenu(ctx, c, chat, notice)
		}
		_ = b.d.Backend.SetMenu(ctx, chat, msg)
		return nil
	})
}

func (b *Bot) linkByCode(ctx context.Context, w *words, chat int64, name, code string) string {
	id, err := b.d.Backend.UserOfCode(ctx, code)
	switch {
	case errors.Is(err, tgate.ErrLinkExpired):
		return w.linkExpired
	case err != nil:
		return w.linkInvalid
	}
	return b.link(ctx, w, chat, name, id)
}

// linkByAddress takes the subscription address in a message: the panel's own, or one of
// the panel the users came from. "" when the message has no address with a path at all;
// an address that leads to no subscription is told so.
func (b *Bot) linkByAddress(ctx context.Context, w *words, chat int64, name, text string) string {
	var seen bool
	for _, raw := range linkInText.FindAllString(text, maxLinksRead) {
		u, err := url.Parse(strings.TrimRight(raw, ".,;:!?)]}>\"'»"))
		if err != nil || u.Host == "" {
			continue
		}
		if u.Path != "" && u.Path != "/" {
			seen = true
		}
		if id, ok := b.d.Backend.UserOfAddress(ctx, u.Path); ok {
			return b.link(ctx, w, chat, name, id)
		}
	}
	if seen {
		return w.linkInvalid
	}
	return ""
}

// link ties a subscription to the chat's account. A subscription has one owner. The link
// to it is no proof of being the owner (it gets forwarded, shown on screenshots), so one
// that is already linked is moved only with its owner's yes, asked in the owner's own chat;
// an owner who cannot be asked, as they blocked the bot, is not in the way.
func (b *Bot) link(ctx context.Context, w *words, chat int64, name string, userID int64) string {
	res, err := b.d.Backend.Link(ctx, chat, userID)
	switch {
	case err != nil:
		return w.linkInvalid
	case res.Status == LinkAlready:
		return fmt.Sprintf(w.alreadyLinked, res.User.Name)
	case res.Status == LinkLimit:
		return fmt.Sprintf(w.linkLimit, tgate.MaxLinks)
	case res.Status == LinkTaken:
		return b.askOwner(ctx, w, chat, name, res.User, res.Owner)
	}
	return fmt.Sprintf(w.linked, res.User.Name)
}

// who is how a Telegram account is named to the owner of a subscription it asks for.
func who(u *tgate.User) string {
	switch {
	case u == nil:
		return "?"
	case u.Username != "":
		return "@" + u.Username
	case u.FirstName != "":
		return u.FirstName
	}
	return strconv.FormatInt(u.ID, 10)
}

// transfer is another account's request for a subscription, open until the owner answers
// or the time is up.
type transfer struct {
	to    int64
	until time.Time
}

const (
	transferTTL    = time.Hour
	transferRefuse = 24 * time.Hour // after a refusal, the same account does not ask again for this long
)

func refusedKey(userID, chat int64) string {
	return strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(chat, 10)
}

// askOwner sends the subscription's owner the question and tells the one who asked to wait.
func (b *Bot) askOwner(ctx context.Context, w *words, chat int64, name string, u db.User, owner int64) string {
	now := b.d.Now()
	b.mu.Lock()
	t, open := b.transfers[u.ID]
	if open && now.After(t.until) {
		delete(b.transfers, u.ID)
		open = false
	}
	switch {
	case open && t.to == chat:
		b.mu.Unlock()
		return w.transferAsked
	case open:
		b.mu.Unlock()
		return w.transferBusy
	case now.Before(b.refused[refusedKey(u.ID, chat)]):
		b.mu.Unlock()
		return w.transferDeniedNew
	}
	b.transfers[u.ID] = transfer{to: chat, until: now.Add(transferTTL)}
	b.mu.Unlock()
	text := html.EscapeString(fmt.Sprintf(w.transferAsk, u.Name, name))
	data := strconv.FormatInt(u.ID, 10) + ":" + strconv.FormatInt(chat, 10)
	kb := &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.transferAllow, CallbackData: "ta:" + data}, {Text: w.transferDeny, CallbackData: "tx:" + data}}}}
	b.d.Outbox.Notice(owner, func(ctx context.Context, c *tgate.Client) error {
		_, err := c.Send(ctx, owner, text, kb, false)
		return err
	}, func(err error) {
		if err != nil { // the owner was not reached: the request lapses, and can be made again
			b.mu.Lock()
			delete(b.transfers, u.ID)
			b.mu.Unlock()
		}
	})
	return w.transferAsked
}

// onTransfer is the owner's answer to askOwner. It counts only from the owner's own chat,
// for the very request that is open, and while it is.
func (b *Bot) onTransfer(ctx context.Context, q *tgate.CallbackQuery) {
	chat, msg := q.Message.Chat.ID, q.Message.MessageID
	b.answer(ctx, q.ID)
	cmd, arg, _ := strings.Cut(q.Data, ":")
	uidStr, toStr, _ := strings.Cut(arg, ":")
	userID, _ := strconv.ParseInt(uidStr, 10, 64)
	to, _ := strconv.ParseInt(toStr, 10, 64)
	w := wordsFor(b.lang(ctx))
	say := func(text string) { // the question's message becomes the answer, without buttons
		b.d.Outbox.Press(chat, "edit:"+strconv.FormatInt(chat, 10)+":"+strconv.FormatInt(msg, 10), func(ctx context.Context, c *tgate.Client) error {
			return c.Edit(ctx, chat, msg, html.EscapeString(text), nil)
		})
	}
	tell := func(to int64, text string) {
		b.d.Outbox.Notice(to, func(ctx context.Context, c *tgate.Client) error {
			_, err := c.Send(ctx, to, html.EscapeString(text), nil, false)
			return err
		}, nil)
	}
	now := b.d.Now()
	// Asked before the lock: over the Panel API it is a call, which must not hold the bot up.
	owner, err := b.d.Backend.Owner(ctx, userID)
	b.mu.Lock()
	t, open := b.transfers[userID]
	if !open || t.to != to || now.After(t.until) || err != nil || owner != chat {
		b.mu.Unlock()
		say(w.transferStale)
		return
	}
	delete(b.transfers, userID)
	if cmd == "tx" {
		b.refused[refusedKey(userID, to)] = now.Add(transferRefuse)
		if len(b.refused) > 1000 {
			for k, until := range b.refused {
				if now.After(until) {
					delete(b.refused, k)
				}
			}
		}
	}
	b.mu.Unlock()
	u, err := b.d.Backend.User(ctx, userID)
	if err != nil {
		say(w.transferStale)
		return
	}
	if cmd == "tx" {
		say(fmt.Sprintf(w.transferDenied, u.Name))
		tell(to, w.transferDeniedNew)
		return
	}
	switch err := b.d.Backend.Transfer(ctx, userID, to); {
	case errors.Is(err, ErrLinkLimit):
		say(w.transferStale)
		tell(to, fmt.Sprintf(w.linkLimit, tgate.MaxLinks))
		return
	case err != nil:
		say(w.transferStale)
		return
	}
	say(fmt.Sprintf(w.transferDone, u.Name))
	b.freshMenu(to, fmt.Sprintf(w.transferNotice, u.Name))
}

// subs are the chat's subscriptions and the one it shows.
func (b *Bot) subs(ctx context.Context, chat int64) ([]db.User, db.User, bool) {
	acc, err := b.d.Backend.Context(ctx, chat)
	list := acc.Links
	if err != nil || len(list) == 0 {
		return nil, db.User{}, false
	}
	cur := list[0]
	if acc.HasChat {
		for _, u := range list {
			if u.ID == acc.Chat.Current {
				cur = u
			}
		}
	}
	return list, cur, true
}

func (b *Bot) miniAppURL(ctx context.Context, cfg Config) string {
	return miniAppURL(ctx, b.d.Backend, cfg)
}

// miniAppURL is where the Mini App opens while the admin keeps its button on; "" when
// Telegram could not load it.
func miniAppURL(ctx context.Context, be Backend, cfg Config) string {
	if !cfg.MiniApp {
		return ""
	}
	return be.MiniAppURL(ctx)
}

func labelOf(cfg Config, action, def string) string {
	for _, btn := range cfg.Buttons {
		if btn.Action == action {
			return btn.Label
		}
	}
	return def
}
