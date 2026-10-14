package tgbot

import (
	"context"
	"html"
	"strconv"
	"strings"

	"subsbot/internal/db"
	"subsbot/internal/plus"
	"subsbot/internal/tgate"
)

// The admin's own menu (plus.Menu): a tree of screens drawn in place of mikan's main menu
// and welcome, and the admin's buttons wherever they are (the menu's screens, the automatic
// messages, the broadcasts).
//
// Callback data: k:<screen>[:<from>] opens a screen of the tree, from the screen it was
// opened from (for «Назад»); kn:<data> opens the screen of data as a new menu message — a
// button under a message of the admin's, which stays as it is.

const (
	treeCode  = "k"
	freshCode = "kn"
)

// Extras are what the bot has besides the look: the admin's menu, the automatic messages,
// the list of the chats, the panel's subscribers and the broadcasts.
type Extras struct {
	Menu       *plus.Doc[plus.Menu]
	Auto       *plus.Doc[plus.Auto]
	Chats      *plus.Chats
	Users      *plus.Users
	Broadcasts *plus.Broadcasts
}

// PlusExtras turns the extras on; call it after Plus.
func (b *Bot) PlusExtras(e Extras) {
	if b.x == nil {
		return
	}
	b.x.e = e
	if e.Chats != nil {
		b.d.Outbox.OnGone(e.Chats.Blocked)
	}
}

// menuTree is the admin's menu when it is on.
func (b *Bot) menuTree() *plus.Menu {
	if b.x == nil || b.x.e.Menu == nil {
		return nil
	}
	if m := b.x.e.Menu.Get(); m.Active() {
		return m
	}
	return nil
}

// subsOf is what a screen of the tree needs of the chat's subscriptions.
type subsOf struct {
	list []db.User
	u    db.User
	ok   bool
	// n is how many subscriptions the account has, when list is not read (a message of the
	// admin's: the panel's list of subscribers tells it).
	n int
}

func (s subsOf) count() int { return max(len(s.list), s.n) }

// treeFor draws the screen of the admin's menu a press asks for: k:<id>, or the main menu
// (m). shown is false when the tree has nothing for it: mikan's screen goes. A screen of the
// tree opens by its button even while the menu is off: a broadcast may point at it.
func (b *Bot) treeFor(ctx context.Context, cfg Config, w *words, chat int64, cmd, arg, notice string, s subsOf) (string, *tgate.Keyboard, bool) {
	if b.x == nil || b.x.e.Menu == nil {
		return "", nil, false
	}
	m := b.x.e.Menu.Get()
	if cmd == treeCode {
		id, from, _ := strings.Cut(arg, ":")
		if sc, ok := m.Screen(id); ok {
			return b.drawTree(ctx, cfg, w, chat, m, sc, from, notice, s)
		}
		cmd = "m" // gone since: the main menu
	}
	if cmd != "m" && cmd != "" || !m.Active() {
		return "", nil, false
	}
	root := m.Main
	if !s.ok {
		root = m.Welcome
	}
	if sc, ok := m.Screen(root); ok {
		return b.drawTree(ctx, cfg, w, chat, m, sc, "", notice, s)
	}
	return "", nil, false
}

// drawTree draws one screen of the tree.
func (b *Bot) drawTree(ctx context.Context, cfg Config, w *words, chat int64, m *plus.Menu, sc plus.Screen, from, notice string, s subsOf) (string, *tgate.Keyboard, bool) {
	vars := b.chatVars(ctx, w, chat, s)
	text := sc.Text
	switch {
	case text == "" && sc.ID == m.Main && s.ok:
		text = pick(cfg.Texts.Main, w.main)
	case text == "" && sc.ID == m.Welcome:
		text = pick(cfg.Texts.Welcome, w.welcome)
	case text == "":
		text = "{brand}"
	}
	out := render(text, vars)
	if notice != "" {
		out = html.EscapeString(notice) + "\n\n" + out
	}
	rows := b.actionRows(ctx, cfg, w, chat, sc.Rows, sc.ID, false, s)
	root := sc.ID == m.Main || sc.ID == m.Welcome
	if sc.ID == m.Main && s.ok && s.count() > 1 && !hasDoing(sc.Rows, "subs") {
		rows = append(rows, []tgate.Button{{Text: subsLabel(w, s.count()), CallbackData: "w"}})
	}
	if sc.Back && !root {
		back := "m"
		if _, ok := m.Screen(from); ok && from != sc.ID && from != m.Main && from != m.Welcome {
			back = treeCode + ":" + from
		}
		rows = append(rows, []tgate.Button{{Text: w.back, CallbackData: back}})
	}
	if len(rows) == 0 {
		return out, nil, true
	}
	return out, &tgate.Keyboard{InlineKeyboard: rows}, true
}

func hasDoing(rows [][]plus.Action, do string) bool {
	for _, r := range rows {
		for _, a := range r {
			if a.Do == do {
				return true
			}
		}
	}
	return false
}

func subsLabel(w *words, n int) string { return w.subscriptions + " (" + strconv.Itoa(n) + ")" }

// chatVars are the variables of the admin's texts for a chat: the subscription's when it
// has one, and the brand and the account's first name always.
func (b *Bot) chatVars(ctx context.Context, w *words, chat int64, s subsOf) map[string]string {
	var vars map[string]string
	if s.ok {
		vars = b.vars(ctx, w, s.u, b.d.Now())
	} else {
		vars = map[string]string{"brand": b.brand(ctx)}
	}
	vars["first_name"] = b.firstName(chat)
	return vars
}

// firstName is the account's first name as the bot last saw it.
func (b *Bot) firstName(chat int64) string {
	if b.x != nil && b.x.e.Chats != nil {
		if ch, ok := b.x.e.Chats.Get(chat); ok {
			return ch.Name
		}
	}
	return ""
}

// actionRows are the admin's buttons as Telegram's: a button that has nothing to do for this
// chat now (the trial taken, no subscription to show) is left out, and an empty row with it.
// fresh: the buttons sit under a message of the admin's and open their screen as a new menu.
func (b *Bot) actionRows(ctx context.Context, cfg Config, w *words, chat int64, rows [][]plus.Action, cur string, fresh bool, s subsOf) [][]tgate.Button {
	var out [][]tgate.Button
	for _, r := range rows {
		var row []tgate.Button
		for _, a := range r {
			if btn, ok := b.actionButton(ctx, cfg, w, chat, a, cur, fresh, s); ok {
				row = append(row, btn)
			}
		}
		if len(row) > 0 {
			out = append(out, row)
		}
	}
	return out
}

// actionButton is one of the admin's buttons for chat; ok is false when it has nothing to do
// there now. Under a message of the admin's (fresh) what only the panel can tell of an account
// that is not in the chat (the trial, the free plan, the packages) is not asked: the screen
// says it when the button is pressed.
func (b *Bot) actionButton(ctx context.Context, cfg Config, w *words, chat int64, a plus.Action, cur string, fresh bool, s subsOf) (tgate.Button, bool) {
	btn := tgate.Button{Text: a.Label, Icon: a.Icon, Style: a.Style}
	code := ""
	switch a.Do {
	case "screen":
		if b.x == nil || b.x.e.Menu == nil {
			return btn, false
		}
		if _, ok := b.x.e.Menu.Get().Screen(a.To); !ok {
			return btn, false
		}
		code = treeCode + ":" + a.To
		if cur != "" && !fresh {
			code += ":" + cur
		}
	case "menu":
		code = "m"
	case "sub", "devices", "connect", "renew":
		if !s.ok {
			return btn, false
		}
		code = map[string]string{"sub": "s", "devices": "d", "connect": "c", "renew": "r"}[a.Do]
	case "traffic":
		if !s.ok || !fresh && len(b.packageOffers(ctx, s.u.ID)) == 0 {
			return btn, false
		}
		code = "x"
	case "subs":
		if !s.ok || s.count() < 2 {
			return btn, false
		}
		code = "w"
	case "buy":
		if !b.canBuyNew(ctx) {
			return btn, false
		}
		code = "b"
	case "trial":
		if fresh {
			if s.ok {
				return btn, false // a customer: the panel gives no trial
			}
		} else if shop := b.d.Backend.Shop(); !shop.TrialOpen(ctx, chat) {
			if _, _, paid := shop.TrialOffer(ctx, chat); !paid {
				return btn, false
			}
		}
		code = "tr"
	case "free":
		if !fresh {
			if _, ok := b.freeButton(ctx, cfg, w, chat); !ok {
				return btn, false
			}
		}
		code = "fp"
	case "about":
		if _, ok := b.about(); !ok {
			return btn, false
		}
		code = aboutCode
	case "support":
		sup := b.supportURL(ctx)
		if sup == "" {
			return btn, false
		}
		btn.URL = sup
		return btn, true
	case "app", "promo":
		u := b.miniAppURL(ctx, cfg)
		if u == "" {
			return btn, false
		}
		if a.Do == "promo" {
			u += "#promocodes"
		}
		btn.WebApp = &tgate.WebApp{URL: u}
		return btn, true
	case "url":
		if !tgate.SafeURL(a.URL) {
			return btn, false
		}
		btn.URL = a.URL
		return btn, true
	default:
		return btn, false
	}
	if fresh {
		code = freshCode + ":" + code
	}
	btn.CallbackData = code
	return btn, len(code) <= 64
}

// onFresh opens the screen of data as a new menu message: a button under a message of the
// admin's was pressed, and that message stays as it is.
func (b *Bot) onFresh(ctx context.Context, q *tgate.CallbackQuery, data string) {
	chat := q.Message.Chat.ID
	b.answer(ctx, q.ID)
	screen, notice := b.act(ctx, chat, data)
	b.d.Outbox.Reply(chat, "menu:"+strconv.FormatInt(chat, 10), 1, func(ctx context.Context, c *tgate.Client) error {
		text, kb := b.screen(ctx, b.config(ctx), chat, screen, notice)
		sent, err := c.Send(ctx, chat, text, kb, false)
		if err != nil {
			return err
		}
		if old, err := b.d.Backend.Chat(ctx, chat); err == nil && old.MenuMsgID != 0 && old.MenuMsgID != sent.MessageID {
			b.tidy(chat, old.MenuMsgID)
		}
		_ = b.d.Backend.SetMenu(ctx, chat, sent.MessageID)
		return nil
	})
}

// seen puts the account of an update in the bot's list of chats.
func (b *Bot) seen(up tgate.Update) {
	if b.x == nil || b.x.e.Chats == nil {
		return
	}
	switch {
	case up.Message != nil && up.Message.From != nil:
		text := strings.TrimSpace(up.Message.Text)
		start := text == "/start" || strings.HasPrefix(text, "/start ")
		b.x.e.Chats.Seen(up.Message.From.ID, up.Message.From.FirstName, start)
	case up.CallbackQuery != nil:
		b.x.e.Chats.Seen(up.CallbackQuery.From.ID, up.CallbackQuery.From.FirstName, false)
	}
}
