package tgbot

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"subsbot/internal/db"
	"subsbot/internal/events"
	"subsbot/internal/plus"
	"subsbot/internal/tgate"
)

// The automatic messages (plus.Auto): what the bot says by itself after something
// happened. Two kinds of trigger:
//
//   - the panel's events (a payment, an expiring or ended subscription, the traffic): the
//     message goes at once from HandleEvent, or waits in the account's line in the list of
//     chats (plus.Due) for its delay;
//   - the bot's own (the first /start, no subscription since): the list of chats is looked
//     over every half minute.
//
// A rule acts only on what happens after it was made, and once per account and event: the
// list of chats keeps what each rule sent.

const autoEvery = 30 * time.Second

// staleDue: a message whose time passed this long ago while the bot was off does not go:
// it would come out of the blue.
const staleDue = 24 * time.Hour

// autoState is what the automatic messages keep in memory.
type autoState struct {
	mu     sync.Mutex
	told   map[string]time.Time // a warning by its text, once an hour
	forgot time.Time            // when the marks of removed rules were last dropped
}

func (s *autoState) warn(log interface{ Warn(string, ...any) }, msg string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.told == nil {
		s.told = map[string]time.Time{}
	}
	if time.Since(s.told[msg]) < time.Hour {
		return
	}
	s.told[msg] = time.Now()
	log.Warn(msg, args...)
}

// AutoRun runs the automatic messages until ctx ends.
func (b *Bot) AutoRun(ctx context.Context) {
	if b.x == nil || b.x.e.Auto == nil || b.x.e.Chats == nil {
		return
	}
	t := time.NewTicker(autoEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.autoTick(ctx)
		}
	}
}

// autoTick sends what is due now.
func (b *Bot) autoTick(ctx context.Context) {
	e := b.x.e
	if !e.Auto.Loaded() || !e.Chats.Loaded() || !b.Holds(ctx) {
		return // nothing goes, nothing is marked sent, while the bot is another addon's
	}
	a := e.Auto.Get()
	now := b.d.Now()
	for _, it := range e.Chats.TakeDue(now) {
		r, ok := a.Rule(it.Rule)
		if !ok || !r.On || now.Unix()-it.At > int64(staleDue/time.Second) {
			continue
		}
		b.sendDue(ctx, r, it)
	}
	var own, ends []plus.Rule
	for _, r := range a.Rules {
		switch {
		case r.On && (r.Trigger == "start" || r.Trigger == "no_sub"):
			own = append(own, r)
		case r.On && (r.Trigger == "before_end" || r.Trigger == "after_end"):
			ends = append(ends, r)
		}
	}
	if len(own) > 0 {
		b.ownTriggers(ctx, own, now)
	}
	if len(ends) > 0 {
		b.endTriggers(ctx, ends, now)
	}
	b.x.auto.mu.Lock()
	forget := now.Sub(b.x.auto.forgot) > 24*time.Hour
	if forget {
		b.x.auto.forgot = now
	}
	b.x.auto.mu.Unlock()
	if forget {
		// A mark is the rule's id, or "<rule>:<subscription>" for the terms the bot counts.
		e.Chats.Forget(func(mark string) bool { id, _, _ := strings.Cut(mark, ":"); _, ok := a.Rule(id); return ok })
		if e.Stats != nil {
			e.Stats.Forget(func(key string) bool {
				kind, id, _ := strings.Cut(key, ".")
				if kind != "r" {
					return true
				}
				_, ok := a.Rule(id)
				return ok
			})
		}
	}
}

// ownTriggers sends the rules of the first /start: to the newcomers whose time came since
// the rule was made.
func (b *Bot) ownTriggers(ctx context.Context, rules []plus.Rule, now time.Time) {
	e := b.x.e
	var byTg map[int64][]plus.Sub
	usersOK := false
	if e.Users != nil {
		var at time.Time
		var err error
		byTg, at, err = e.Users.Snapshot()
		usersOK = !at.IsZero() && err == nil
	}
	t := now.Unix()
	type send struct {
		tg int64
		r  plus.Rule
	}
	var out []send
	e.Chats.Each(func(tg int64, ch plus.Chat) {
		if !ch.Start || ch.First == 0 || ch.Blocked != 0 {
			return
		}
		for _, r := range rules {
			due := ch.First + r.Delay*60
			if ch.First < r.Created || due > t || t-due > int64(staleDue/time.Second) || ch.Sent[r.ID] != 0 {
				continue
			}
			if r.Trigger == "no_sub" {
				if !usersOK {
					b.x.auto.warn(b.d.Log, "auto: «не оформил подписку» needs the permission «Все пользователи» to tell who bought")
					continue
				}
				if len(byTg[tg]) > 0 {
					continue
				}
			}
			out = append(out, send{tg, r})
		}
	})
	// The list may be minutes old. A few accounts are asked about one by one; many at once
	// (after a wave of /start) read the whole list again instead: one pass, not a search each.
	noSub := 0
	for _, s := range out {
		if s.r.Trigger == "no_sub" {
			noSub++
		}
	}
	fresh := noSub > 20 && e.Users.Refresh(ctx) == nil
	if fresh {
		byTg, _, _ = e.Users.Snapshot()
	}
	for _, s := range out {
		if s.r.Trigger == "no_sub" {
			var subs []plus.Sub
			var err error
			if fresh {
				subs = byTg[s.tg]
			} else {
				subs, err = e.Users.OfTg(ctx, s.tg)
			}
			if err != nil || len(subs) > 0 {
				if err == nil {
					e.Chats.MarkSent(s.tg, s.r.ID, t) // bought meanwhile: never again
				}
				continue
			}
		}
		e.Chats.MarkSent(s.tg, s.r.ID, t)
		pm := b.prepareMsg(ctx, s.r.Message, "r."+s.r.ID)
		b.queueAdmin(s.tg, pm, b.autoVars(ctx, wordsFor(b.config(ctx).Lang), pm.brand, s.tg, nil, nil), false, "notice", nil)
	}
}

// autoEvent runs the rules that follow event e of the panel. u is its subscription as
// HandleEvent read it (zero when there is none). replace: a rule takes the place of the
// panel's own notice of it.
func (b *Bot) autoEvent(ctx context.Context, e events.Event, u db.User) (replace bool) {
	if b.x == nil || b.x.e.Auto == nil || b.x.e.Chats == nil || e.TgID == 0 {
		return false
	}
	trig, days, kind := "", 0, ""
	switch e.Type {
	case events.PaymentPaid:
		var p events.Payment
		if json.Unmarshal(e.Data, &p) != nil {
			return false
		}
		if p.Kind != "new" && p.Kind != "renew" {
			return false
		}
		trig, kind = "paid", p.Kind
	case events.UserExpiring:
		var n events.Notice
		_ = json.Unmarshal(e.Data, &n)
		trig, days = "expiring", n.Days
	case events.UserExpired:
		trig = "expired"
	case events.Traffic90:
		trig = "traffic_90"
	case events.Traffic100:
		trig = "traffic_100"
	default:
		return false
	}
	chats := b.x.e.Chats
	now := b.d.Now()
	for _, r := range b.x.e.Auto.Get().Rules {
		switch {
		case !r.On || r.Trigger != trig:
			continue
		case trig == "expiring" && r.Days != 0 && r.Days != days:
			continue
		case trig == "paid" && r.First && kind != "new":
			continue
		case len(r.Tariffs) > 0 && !slices.Contains(r.Tariffs, eventTariff(ctx, b, e, u)):
			continue
		case !e.At.IsZero() && e.At.Unix() < r.Created:
			continue // happened before the rule was made
		}
		// The panel's notice stays away on the event's second delivery too.
		replace = replace || r.Replace
		if e.ID != 0 && chats.Sent(e.TgID, r.ID) >= e.ID {
			continue // this event again (delivery is at least once)
		}
		chats.MarkSent(e.TgID, r.ID, e.ID)
		if r.Delay > 0 {
			chats.AddDue(e.TgID, plus.Due{Rule: r.ID, At: now.Add(time.Duration(r.Delay) * time.Minute).Unix(), User: e.UserID, Event: e.ID})
			continue
		}
		pm := b.prepareMsg(ctx, r.Message, "r."+r.ID)
		b.queueAdmin(e.TgID, pm, b.autoVars(ctx, wordsFor(b.config(ctx).Lang), pm.brand, e.TgID, &u, nil), u.ID != 0, "notice", nil)
	}
	return replace
}

// sendDue sends an automatic message whose delay passed, if what it is about still holds.
func (b *Bot) sendDue(ctx context.Context, r plus.Rule, it plus.DueItem) {
	var sub *plus.Sub
	if it.User != 0 && b.x.e.Users != nil {
		s, err := b.x.e.Users.One(ctx, it.User)
		switch {
		case err == nil:
			sub = &s
		case errors.Is(err, plus.ErrNotFound):
			return // the subscription is gone: nothing to say about it
		case errors.Is(err, plus.ErrNoUsersAll):
			if r.Trigger == "expired" || r.Trigger == "expiring" {
				b.x.auto.warn(b.d.Log, "auto: a delayed message about the term needs the permission «Все пользователи» to tell a renewed subscription")
				return
			}
		default:
			// The panel did not answer: in line again a minute later.
			b.x.e.Chats.AddDue(it.TgID, plus.Due{Rule: it.Rule, At: b.d.Now().Add(time.Minute).Unix(), User: it.User, Event: it.Event})
			return
		}
	}
	if sub != nil && len(r.Tariffs) > 0 && !slices.Contains(r.Tariffs, sub.TariffName) {
		return
	}
	if sub != nil {
		switch r.Trigger {
		case "expired":
			if sub.Works() {
				return // renewed meanwhile
			}
		case "expiring":
			if sub.ExpiresAt == nil || sub.ExpiresAt.After(b.d.Now().Add(4*24*time.Hour)) {
				return // renewed meanwhile
			}
		}
	}
	pm := b.prepareMsg(ctx, r.Message, "r."+r.ID)
	b.queueAdmin(it.TgID, pm, b.autoVars(ctx, wordsFor(b.config(ctx).Lang), pm.brand, it.TgID, nil, sub), sub != nil && sub.Works(), "notice", nil)
}

// autoVars are the variables of an automatic message to tg: those of the subscription u as
// the bot reads it (an event's), or of sub as the panel's list tells it.
func (b *Bot) autoVars(ctx context.Context, w *words, brand string, tg int64, u *db.User, sub *plus.Sub) map[string]string {
	v := b.msgVars(w, brand, tg, sub)
	if u != nil && u.ID != 0 {
		for k, x := range b.vars(ctx, w, *u, b.d.Now()) {
			v[k] = x
		}
		v["first_name"] = b.firstName(tg)
	}
	return v
}

// adminMsg is one of the admin's messages made ready to send: its buttons for those with a
// working subscription and for the rest, and the panel's settings it needs, read once. A
// broadcast to thousands asks the panel nothing per message.
type adminMsg struct {
	m     plus.Message
	tag   string // the message's name in the counts: r.<rule>, m.<key>, b.<job>
	brand string
	quiet bool               // the panel's quiet night
	kb    [2]*tgate.Keyboard // [0] without a subscription, [1] with one
}

func (b *Bot) prepareMsg(ctx context.Context, m plus.Message, tag string) adminMsg {
	cfg := b.config(ctx)
	w := wordsFor(cfg.Lang)
	pm := adminMsg{m: m, tag: tag, brand: b.brand(ctx), quiet: cfg.QuietNight}
	for i, s := range []subsOf{{}, {ok: true, n: 1}} {
		if rows := b.actionRows(ctx, cfg, w, 0, m.Rows, "", true, tag, s); len(rows) > 0 {
			pm.kb[i] = &tgate.Keyboard{InlineKeyboard: rows}
		}
	}
	return pm
}

// queueAdmin puts one of the admin's messages to chat in the outbox, at the notice or bulk
// pace; done hears how it went. vars must hold the brand (adminMsg.vars). The automatic
// notices go silent at night, as the panel's.
func (b *Bot) queueAdmin(chat int64, pm adminMsg, vars map[string]string, withSub bool, lane string, done func(error)) {
	text := render(pm.m.Text, vars)
	kb := pm.kb[0]
	if withSub {
		kb = pm.kb[1]
	}
	send := func(ctx context.Context, c *tgate.Client) error {
		ctx = tgate.WithPriority(ctx, lane)
		silent := pm.quiet && night(b.d.Now())
		if pm.m.Photo != "" {
			_, err := c.SendPhoto(ctx, chat, pm.m.Photo, text, kb, silent)
			return err
		}
		_, err := c.Send(ctx, chat, text, kb, silent)
		return err
	}
	counted := func(err error) {
		if err == nil && b.x != nil && b.x.e.Stats != nil {
			b.x.e.Stats.Sent(pm.tag)
		}
		if done != nil {
			done(err)
		}
	}
	if lane == "bulk" {
		b.d.Outbox.Bulk(chat, send, counted)
		return
	}
	b.d.Outbox.Notice(chat, send, counted)
}

// msgVars are the variables of an admin's message to tg: the brand, the first name, and the
// subscription's from sub (as the panel's list tells it).
func (b *Bot) msgVars(w *words, brand string, tg int64, sub *plus.Sub) map[string]string {
	v := map[string]string{"brand": brand, "first_name": b.firstName(tg)}
	if sub != nil {
		v["name"] = sub.Name
		v["tariff"] = sub.TariffName
		if sub.ExpiresAt != nil {
			exp := sub.ExpiresAt.UTC()
			left := int((exp.Sub(b.d.Now()) + 24*time.Hour - time.Second) / (24 * time.Hour))
			v["until"] = w.date(exp)
			v["days"] = w.days(max(0, left))
		}
	}
	return v
}

// TestMessage sends m to chat now as an automatic message or a broadcast would, for the
// admin to see it. It waits for Telegram's answer.
func (b *Bot) TestMessage(ctx context.Context, chat int64, m plus.Message) error {
	if !b.Holds(ctx) {
		return ErrNotHolder
	}
	done := make(chan error, 1)
	var sub *plus.Sub
	if b.x != nil && b.x.e.Users != nil {
		if subs, err := b.x.e.Users.OfTg(ctx, chat); err == nil {
			sub = pickSub(subs)
		}
	}
	pm := b.prepareMsg(ctx, m, "")
	vars := b.msgVars(wordsFor(b.config(ctx).Lang), pm.brand, chat, sub)
	b.queueAdmin(chat, pm, vars, sub != nil && sub.Works(), "", func(err error) { done <- err })
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Vars lists the variables of the admin's messages, for the page.
var Vars = []struct {
	Key  string `json:"key"`
	Hint string `json:"hint"`
}{
	{"brand", "название сервиса"},
	{"first_name", "имя в Telegram"},
	{"name", "название подписки"},
	{"tariff", "тариф (в автосообщениях о подписке)"},
	{"until", "дата окончания"},
	{"days", "сколько дней осталось"},
}

// eventTariff is the tariff an event is about: the payment's as paid, else the subscription's.
func eventTariff(ctx context.Context, b *Bot, e events.Event, u db.User) string {
	if e.Type == events.PaymentPaid {
		var p events.Payment
		if json.Unmarshal(e.Data, &p) == nil && p.TariffName != "" {
			return p.TariffName
		}
	}
	return b.tariffName(ctx, u.TariffID.Int64, u.TariffID.Valid)
}

// endTriggers sends the rules about the end of a subscription the bot counts itself, from the
// panel's list of subscribers: some time before the end of a subscription that works, and some
// time after the end of one that was not renewed. Once per rule, subscription and term; each
// is asked about again before it goes, as the list may be minutes old.
func (b *Bot) endTriggers(ctx context.Context, rules []plus.Rule, now time.Time) {
	e := b.x.e
	if e.Users == nil {
		return
	}
	byTg, at, err := e.Users.Snapshot()
	if err != nil || at.IsZero() {
		b.x.auto.warn(b.d.Log, "auto: messages about the end of a subscription need the permission «Все пользователи»")
		return
	}
	t := now.Unix()
	w := wordsFor(b.config(ctx).Lang)
	prepared := map[string]adminMsg{}
	for tg, subs := range byTg {
		if ch, ok := e.Chats.Get(tg); ok && ch.Blocked != 0 {
			continue
		}
		for _, s := range subs {
			if s.ExpiresAt == nil {
				continue
			}
			exp := s.ExpiresAt.Unix()
			for _, r := range rules {
				if !endDue(r, s, exp, t) {
					continue
				}
				key := r.ID + ":" + strconv.FormatInt(s.ID, 10)
				if e.Chats.Sent(tg, key) == exp {
					continue
				}
				// The list may be minutes old: renewed meanwhile, nothing to say.
				fresh, err := e.Users.One(ctx, s.ID)
				if err != nil {
					continue
				}
				if fresh.ExpiresAt == nil || fresh.ExpiresAt.Unix() != exp || !endDue(r, fresh, exp, t) {
					continue
				}
				e.Chats.MarkSent(tg, key, exp)
				pm, ok := prepared[r.ID]
				if !ok {
					pm = b.prepareMsg(ctx, r.Message, "r."+r.ID)
					prepared[r.ID] = pm
				}
				b.queueAdmin(tg, pm, b.msgVars(w, pm.brand, tg, &fresh), fresh.Works(), "notice", nil)
			}
		}
	}
}

// endDue says whether rule r is due now (t) for subscription s ending at exp.
func endDue(r plus.Rule, s plus.Sub, exp, t int64) bool {
	if len(r.Tariffs) > 0 && !slices.Contains(r.Tariffs, s.TariffName) {
		return false
	}
	var due int64
	switch r.Trigger {
	case "before_end":
		due = exp - r.Delay*60
		if t >= exp || !s.Works() {
			return false
		}
	case "after_end":
		due = exp + r.Delay*60
		if s.Works() {
			return false
		}
	default:
		return false
	}
	return t >= due && due >= r.Created && t-due <= int64(staleDue/time.Second)
}
