package tgbot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"subsbot/internal/billing"
	"subsbot/internal/db"
	"subsbot/internal/domain"
	"subsbot/internal/tgate"
)

// Callback data: m main, s subscription, d devices, dc:<id> confirm unbind, du:<id>
// unbind, c connect, r renew, p:<button> the admin's page, w switch list, u:<user> show
// that subscription, rm confirm removing it from the account, rd:<user> remove it, rl
// confirm a new link, rn:<user> issue it (reissue.go).

// screen renders what the chat sees for a press: text (HTML) and buttons.
func (b *Bot) screen(ctx context.Context, cfg Config, chat int64, data, notice string) (string, *tgate.Keyboard) {
	w := wordsFor(cfg.Lang)
	cmd, arg, _ := strings.Cut(data, ":")
	// The channel stands in front of every screen but paying (ungated). Asked here, as the
	// screen is drawn: off the update loop, a slow Telegram holds up this chat only.
	switch {
	case cmd == "gc":
		if !b.passes(ctx, cfg, chat, true) {
			return b.gateScreen(ctx, cfg, w, w.channelNotYet)
		}
		cmd = "m"
	case !ungated[cmd] && !b.passes(ctx, cfg, chat, false):
		return b.gateScreen(ctx, cfg, w, notice)
	}
	// plus: «О сервисе», with or without a subscription.
	if cmd == aboutCode {
		if text, kb, ok := b.aboutScreen(ctx, cfg, w, notice); ok {
			return text, kb
		}
	}
	list, u, ok := b.subs(ctx, chat)
	// plus: the admin's own menu, where it has a screen for the press.
	if text, kb, shown := b.treeFor(ctx, cfg, w, chat, cmd, arg, notice, subsOf{list: list, u: u, ok: ok}); shown {
		return text, kb
	}
	// Buying a new subscription works with or without one.
	if b.canBuyNew(ctx) {
		home := []tgate.Button{{Text: w.back, CallbackData: "m"}}
		switch cmd {
		case "b":
			return b.shopList(ctx, w, w.buyTitle, "tn", notice, home)
		case "tn":
			return b.shopTariff(ctx, w, arg, "tn", "pn", shopBack(w, "tn", arg, "b"))
		case "pn":
			id, _, _ := strings.Cut(arg, ":")
			return b.shopInvoice(ctx, w, chat, 0, arg, []tgate.Button{{Text: w.back, CallbackData: "tn:" + id}})
		}
	}
	switch cmd {
	case "tr":
		return b.takeTrial(ctx, w, chat)
	case "tp":
		return b.trialInvoice(ctx, w, chat, arg)
	case "fp":
		return b.takeFree(ctx, cfg, w, chat)
	}
	if !ok {
		// plus: the admin's pages open for those without a subscription too.
		if text, kb, shown := b.welcomePage(ctx, cfg, w, cmd, arg, notice); shown {
			return text, kb
		}
		if text, kb, shown := b.treeFor(ctx, cfg, w, chat, "m", "", notice, subsOf{list: list, u: u, ok: ok}); shown {
			return text, kb // plus: the admin's first screen
		}
		return b.welcome(ctx, cfg, w, chat, notice)
	}
	now := b.d.Now()
	vars := b.vars(ctx, w, u, now)
	back := []tgate.Button{{Text: w.back, CallbackData: "m"}}
	withNotice := func(s string) string {
		if notice != "" {
			return html.EscapeString(notice) + "\n\n" + s
		}
		return s
	}
	switch cmd {
	case "t":
		return b.shopTariff(ctx, w, arg, "t", "py", shopBack(w, "t", arg, "r"))
	case "py":
		id, _, _ := strings.Cut(arg, ":")
		return b.shopInvoice(ctx, w, chat, u.ID, arg, []tgate.Button{{Text: w.back, CallbackData: "t:" + id}})
	case "s":
		lines := []string{"<b>" + html.EscapeString(fmt.Sprintf(w.subTitle, u.Name)) + "</b>", html.EscapeString(vars["state"]), "",
			"📅 " + html.EscapeString(vars["term"]), "📦 " + html.EscapeString(vars["traffic"])}
		for _, p := range b.poolLines(ctx, w, u.ID) {
			lines = append(lines, "📦 "+html.EscapeString(p))
		}
		if r := vars["reset"]; r != "" {
			lines = append(lines, html.EscapeString(fmt.Sprintf(w.resets, r)))
		}
		lines = append(lines, "📱 "+html.EscapeString(vars["devices"]))
		newLink, linkLine := b.reissueRow(ctx, w, u)
		if linkLine != "" {
			lines = append(lines, linkLine)
		}
		// plus: the admin's template of this screen.
		if text, ok := b.subScreenText(ctx, w, u.ID, vars, linkLine); ok {
			lines = []string{text}
		}
		rows := [][]tgate.Button{}
		if offers := b.packageOffers(ctx, u.ID); len(offers) > 0 {
			rows = append(rows, []tgate.Button{{Text: w.buyTraffic, CallbackData: "x"}})
		}
		if newLink != nil {
			rows = append(rows, []tgate.Button{*newLink})
		}
		rows = append(rows, []tgate.Button{{Text: w.removeSub, CallbackData: "rm"}})
		return withNotice(strings.Join(lines, "\n")), &tgate.Keyboard{InlineKeyboard: append(rows, back)}
	case "rm":
		// The id goes with the button: a tap after switching subscriptions removes the
		// one that was asked about, not the one shown now.
		return withNotice(html.EscapeString(fmt.Sprintf(w.confirmRemove, u.Name))), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{
			{{Text: w.yesRemove, CallbackData: "rd:" + strconv.FormatInt(u.ID, 10)}, {Text: w.cancel, CallbackData: "s"}}}}
	case "rl":
		text, kb, ok := b.reissueConfirm(ctx, w, u)
		if !ok {
			// Stale button: the subscription screen says why there is none now.
			return b.screen(ctx, cfg, chat, "s", text)
		}
		return withNotice(text), kb
	case "x", "xk", "xp":
		return b.trafficShop(ctx, w, chat, u, cmd, arg, notice)
	case "d", "dc":
		id, _ := strconv.ParseInt(arg, 10, 64)
		return b.devices(ctx, w, u, cmd, id, notice, now)
	case "c":
		text := "<b>" + w.connectTitle + "</b>\n\n" + fmt.Sprintf(w.connectText, html.EscapeString(b.subURL(ctx, u)))
		if t, ok := b.connectText(ctx, u); ok { // plus: the admin's template
			text = t
		}
		rows := [][]tgate.Button{}
		if btn, ok := b.pageButton(ctx, cfg, w, w.openPage); ok {
			rows = append(rows, []tgate.Button{btn})
		}
		return withNotice(text), &tgate.Keyboard{InlineKeyboard: append(rows, back)}
	case "r":
		if offers, _ := b.offers(ctx); len(offers) > 0 {
			text, kb := b.shopList(ctx, w, fmt.Sprintf(w.renewTitle, u.Name), "t", notice, nil)
			if sup := b.supportURL(ctx); sup != "" {
				kb.InlineKeyboard = append(kb.InlineKeyboard, []tgate.Button{{Text: labelOf(cfg, "support", w.support), URL: sup}})
			}
			kb.InlineKeyboard = append(kb.InlineKeyboard, back)
			return text, kb
		}
		rows := [][]tgate.Button{}
		if sup := b.supportURL(ctx); sup != "" {
			rows = append(rows, []tgate.Button{{Text: labelOf(cfg, "support", w.support), URL: sup}})
		}
		return withNotice(render(pick(cfg.Texts.Renew, w.renew), vars)), &tgate.Keyboard{InlineKeyboard: append(rows, back)}
	case "p":
		for _, btn := range cfg.Buttons {
			if btn.Action == "page" && btn.ID == arg {
				return withNotice(render(btn.Text, vars)), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{back}}
			}
		}
	case "w":
		rows := [][]tgate.Button{}
		for _, s := range list {
			mark := ""
			if s.ID == u.ID {
				mark = "✓ "
			}
			rows = append(rows, []tgate.Button{{Text: mark + s.Name, CallbackData: "u:" + strconv.FormatInt(s.ID, 10)}})
		}
		return w.switchTitle, &tgate.Keyboard{InlineKeyboard: append(rows, back)}
	}
	if text, kb, shown := b.treeFor(ctx, cfg, w, chat, "m", "", notice, subsOf{list: list, u: u, ok: ok}); shown {
		return text, kb // plus: the admin's main menu
	}
	return withNotice(render(pick(cfg.Texts.Main, w.main), vars)), b.menu(ctx, cfg, w, chat, len(list))
}

func (b *Bot) welcome(ctx context.Context, cfg Config, w *words, chat int64, notice string) (string, *tgate.Keyboard) {
	brand := b.brand(ctx)
	text := render(pick(cfg.Texts.Welcome, w.welcome), map[string]string{"brand": brand})
	if notice != "" {
		text = html.EscapeString(notice) + "\n\n" + text
	}
	var rows [][]tgate.Button
	shop := b.d.Backend.Shop()
	if o, _, ok := shop.TrialOffer(ctx, chat); ok {
		rows = append(rows, []tgate.Button{{Text: fmt.Sprintf(w.trialPaid, w.cheapest(billing.Offer{Stars: o.Stars, Rub: o.Rub})), CallbackData: "tr"}})
	} else if shop.TrialOpen(ctx, chat) {
		rows = append(rows, []tgate.Button{{Text: w.trial, CallbackData: "tr"}})
	}
	if btn, ok := b.freeButton(ctx, cfg, w, chat); ok {
		rows = append(rows, []tgate.Button{btn})
	}
	if b.canBuyNew(ctx) {
		rows = append(rows, []tgate.Button{{Text: w.buy, CallbackData: "b"}})
	}
	if sup := b.supportURL(ctx); sup != "" {
		rows = append(rows, []tgate.Button{{Text: labelOf(cfg, "support", w.support), URL: sup}})
	}
	// plus: the admin's links and pages (the agreement, the privacy policy) for newcomers too.
	rows = b.welcomeRows(ctx, cfg, rows)
	if rows == nil {
		return text, nil
	}
	return text, &tgate.Keyboard{InlineKeyboard: rows}
}

// menu is the admin's main menu as buttons.
func (b *Bot) menu(ctx context.Context, cfg Config, w *words, chat int64, subs int) *tgate.Keyboard {
	cfg = b.menuConfig(cfg) // plus: «О сервисе» in place of the documents
	var rows [][]tgate.Button
	for _, mb := range cfg.Buttons {
		if !mb.On {
			continue
		}
		var btn tgate.Button
		switch mb.Action {
		case "sub":
			btn = tgate.Button{Text: mb.Label, CallbackData: "s"}
		case "devices":
			btn = tgate.Button{Text: mb.Label, CallbackData: "d"}
		case "connect":
			btn = tgate.Button{Text: mb.Label, CallbackData: "c"}
		case "renew":
			btn = tgate.Button{Text: mb.Label, CallbackData: "r"}
		case "support":
			sup := b.supportURL(ctx)
			if sup == "" {
				continue
			}
			btn = tgate.Button{Text: mb.Label, URL: sup}
		case "app":
			var ok bool
			if btn, ok = b.pageButton(ctx, cfg, w, mb.Label); !ok {
				continue
			}
		case "url":
			btn = tgate.Button{Text: mb.Label, URL: mb.URL}
		case "page":
			btn = tgate.Button{Text: mb.Label, CallbackData: "p:" + mb.ID}
		case "about": // plus
			btn = tgate.Button{Text: mb.Label, CallbackData: aboutCode}
		default:
			continue
		}
		if mb.Row && len(rows) > 0 && len(rows[len(rows)-1]) < 3 {
			rows[len(rows)-1] = append(rows[len(rows)-1], btn)
		} else {
			rows = append(rows, []tgate.Button{btn})
		}
	}
	// The free plan comes back to the menu once the account's free subscription has ended.
	if btn, ok := b.freeButton(ctx, cfg, w, chat); ok {
		rows = append(rows, []tgate.Button{btn})
	}
	// The promo codes entry is the admin's to hide; the Mini App page keeps its field.
	if url := b.miniAppURL(ctx, cfg); url != "" && cfg.PromoButton {
		rows = append(rows, []tgate.Button{{Text: w.promo, WebApp: &tgate.WebApp{URL: url + "#promocodes"}}})
	}
	if subs > 1 {
		rows = append(rows, []tgate.Button{{Text: fmt.Sprintf("%s (%d)", w.subscriptions, subs), CallbackData: "w"}})
	}
	return &tgate.Keyboard{InlineKeyboard: rows}
}

// pageButton opens the subscription page: in the Mini App when Telegram can load it,
// else in the browser.
func (b *Bot) pageButton(ctx context.Context, cfg Config, w *words, label string) (tgate.Button, bool) {
	if url := b.miniAppURL(ctx, cfg); url != "" {
		return tgate.Button{Text: label, WebApp: &tgate.WebApp{URL: url}}, true
	}
	return tgate.Button{}, false
}

func (b *Bot) devices(ctx context.Context, w *words, u db.User, cmd string, id int64, notice string, now time.Time) (string, *tgate.Keyboard) {
	back := []tgate.Button{{Text: w.back, CallbackData: "m"}}
	binding := b.d.Backend.DeviceBinding(ctx)
	head := "<b>" + w.devicesTitle + "</b> · " + html.EscapeString(b.vars(ctx, w, u, now)["devices"])
	if !binding {
		limit := "∞"
		if u.DeviceLimit.Valid {
			limit = strconv.FormatInt(u.DeviceLimit.Int64, 10)
		}
		return head + "\n\n" + html.EscapeString(fmt.Sprintf(w.devicesOff, limit)), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{back}}
	}
	devs, err := b.d.Backend.Devices(ctx, u.ID)
	if err != nil {
		devs = nil
	}
	used, err := b.d.Backend.DeviceTraffic(ctx, u.ID)
	if err != nil {
		used = map[int64]int64{}
	}
	name := func(d db.BoundDevice) string { return deviceName(w, d) }
	rules, err := b.d.Backend.UnbindState(ctx, u.ID)
	if err != nil {
		b.d.Log.Warn("telegram: unbind rules", "err", err)
	}
	// No unbind buttons while none is left, or when only the admin unbinds.
	waiting := !rules.Next.IsZero() || rules.Rules.Off
	if cmd == "dc" && !waiting {
		for _, d := range devs {
			if d.ID == id {
				text := html.EscapeString(fmt.Sprintf(w.confirmUnbind, name(d)))
				switch r := rules.Rules.ReturnHours; {
				case d.Hwid == "":
				case r == domain.ReturnNever:
					text += "\n\n" + html.EscapeString(w.confirmNever)
				case r > 0:
					text += "\n\n" + html.EscapeString(fmt.Sprintf(w.confirmReturn, w.span(r)))
				}
				return text, &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{
					{{Text: w.yesUnbind, CallbackData: "du:" + strconv.FormatInt(id, 10)}, {Text: w.cancel, CallbackData: "d"}}}}
			}
		}
	}
	lines := []string{head, ""}
	switch {
	case notice != "":
		lines = append([]string{html.EscapeString(notice), ""}, lines...)
	case !rules.Next.IsZero() && len(devs) > 0:
		// No buttons to tap while the limit is reached: the line says when they come back.
		lines = append([]string{"<b>" + html.EscapeString(fmt.Sprintf(w.wait, b.when(w, rules.Next))) + "</b>", ""}, lines...)
	}
	rows := [][]tgate.Button{}
	if len(devs) == 0 {
		lines = append(lines, html.EscapeString(w.devicesNone))
	}
	for i, d := range devs {
		meta := []string{}
		if d.Name != "" && d.Model != "" {
			meta = append(meta, d.Model)
		}
		if d.Hwid != "" && d.Model != "" && d.Os != "" {
			meta = append(meta, strings.TrimSpace(d.Os+" "+d.OsVersion))
		}
		if app, _, _ := strings.Cut(strings.TrimSpace(d.App), " "); app != "" {
			meta = append(meta, strings.Replace(app, "/", " ", 1))
		}
		if n := used[d.ID]; n > 0 {
			meta = append(meta, w.bytes(n))
		}
		meta = append(meta, w.ago(time.Unix(d.LastSeen, 0), now))
		lines = append(lines, fmt.Sprintf("%d. %s — %s", i+1, html.EscapeString(name(d)), html.EscapeString(strings.Join(meta, " · "))))
		if !waiting {
			rows = append(rows, []tgate.Button{{Text: "❌ " + name(d), CallbackData: "dc:" + strconv.FormatInt(d.ID, 10)}})
		}
	}
	if len(devs) > 0 {
		note := []string{w.devicesNote}
		if !domain.BindsDevices(u.DeviceMode) {
			note = append(note, linkWordsFor(w).onlineNote)
		}
		switch r := rules.Rules.ReturnHours; {
		case rules.Rules.Off:
			note = append(note, w.unbindOff)
		case r == domain.ReturnNever:
			note = append(note, w.returnNever)
		case r > 0:
			note = append(note, fmt.Sprintf(w.returnNote, w.span(r)))
		}
		if left := rules.Left(); left >= 0 && !waiting {
			note = append(note[:1], append([]string{fmt.Sprintf(w.unbindLeft, left, rules.Rules.Limit, w.period(rules.Rules.Days))}, note[1:]...)...)
		}
		lines = append(lines, "", html.EscapeString(strings.Join(note, " ")))
	}
	// plus: the admin's templates of this screen.
	if text, ok := b.devicesText(ctx, w, u, devs, used, rules, waiting, notice, now); ok {
		return text, &tgate.Keyboard{InlineKeyboard: append(rows, back)}
	}
	return strings.Join(lines, "\n"), &tgate.Keyboard{InlineKeyboard: append(rows, back)}
}

// deviceName: the device's own name (the admin's or the subscriber's, escaped where it is
// shown), else what its app reported.
func deviceName(w *words, d db.BoundDevice) string {
	switch {
	case d.Name != "":
		return d.Name
	case d.Hwid == "":
		return w.sharedPlace
	case d.Model != "":
		return d.Model
	case d.Os != "":
		return strings.TrimSpace(d.Os + " " + d.OsVersion)
	}
	return w.device
}

// act does what a tap changes — unbinding a device, showing another subscription — and
// says which screen to draw after it, with a line on top. A tap's effect never waits in
// the outbox: only its screen does, and a later tap may replace that.
func (b *Bot) act(ctx context.Context, chat int64, data string) (screen, notice string) {
	cmd, arg, _ := strings.Cut(data, ":")
	id, _ := strconv.ParseInt(arg, 10, 64)
	switch cmd {
	case "du":
		w := wordsFor(b.config(ctx).Lang)
		_, u, ok := b.subs(ctx, chat)
		if !ok {
			return "m", ""
		}
		devs, _ := b.d.Backend.Devices(ctx, u.ID)
		for _, d := range devs {
			if d.ID != id {
				continue
			}
			err := b.d.Backend.Unbind(ctx, u.ID, id)
			switch {
			case err == nil:
				b.d.Log.Info("telegram: the subscriber unbound a device", "user", u.ID, "device", id)
				return "d", fmt.Sprintf(w.unbound, deviceName(w, d))
			case errors.Is(err, domain.ErrUnbindCooldown):
				st, _ := b.d.Backend.UnbindState(ctx, u.ID)
				return "d", fmt.Sprintf(w.wait, b.when(w, st.Next))
			case errors.Is(err, domain.ErrNotFound):
				return "d", ""
			case errors.Is(err, domain.ErrUnbindOff):
				return "d", w.unbindOff
			default:
				b.d.Log.Warn("telegram: device not unbound", "user", u.ID, "device", id, "err", err)
				return "d", w.unbindFailed
			}
		}
		return "d", ""
	case "rn":
		return b.reissueAct(ctx, chat, id)
	case "u", "xu":
		// "xu" is the buy button of a traffic notice: that subscription's packages, not the
		// one the menu shows now. Only a subscription of this account is switched to.
		list, _, _ := b.subs(ctx, chat)
		for _, s := range list {
			if s.ID == id {
				_ = b.d.Backend.SetCurrent(ctx, chat, id)
				if cmd == "xu" {
					return "x", ""
				}
			}
		}
		return "m", ""
	case "rd":
		// Only a subscription of this account, and only its link: the subscription itself
		// stays as it is. The next one in the list is shown after it.
		w := wordsFor(b.config(ctx).Lang)
		list, _, _ := b.subs(ctx, chat)
		for _, s := range list {
			if s.ID != id {
				continue
			}
			if err := b.d.Backend.Unlink(ctx, id); err != nil {
				b.d.Log.Warn("telegram: remove a subscription", "user", id, "err", err)
				return "s", ""
			}
			b.d.Log.Info("telegram: a subscription removed from its account", "user", id)
			return "m", fmt.Sprintf(w.removed, s.Name)
		}
		return "m", ""
	}
	return data, ""
}

func (b *Bot) when(w *words, t time.Time) string {
	if t.IsZero() {
		return w.justNow
	}
	return w.shortDate(t) + " " + t.Format("15:04") + " UTC"
}

// vars are the {variables} of the admin's texts for one subscription.
func (b *Bot) vars(ctx context.Context, w *words, u db.User, now time.Time) map[string]string {
	grants, err := b.d.Backend.GrantsLeft(ctx, u.ID, now)
	if err != nil {
		b.d.Log.Warn("telegram: traffic packages", "err", err)
	}
	extra := grants.Main(u.ID)
	state := domain.State(u, extra, now)
	v := map[string]string{"name": u.Name, "brand": b.brand(ctx), "until": w.forever, "days": "—", "term": w.forever, "reset": ""}
	switch state {
	case domain.StateActive:
		v["state"] = w.stateActive
	case domain.StateExpiring:
		v["state"] = w.stateExpiring
	case domain.StateLimited:
		v["state"] = w.stateLimited
	case domain.StateExpired:
		v["state"] = w.stateExpired
	default:
		v["state"] = w.stateOff
	}
	if u.ExpiresAt.Valid {
		exp := time.Unix(u.ExpiresAt.Int64, 0).UTC()
		left := int((exp.Sub(now) + 24*time.Hour - time.Second) / (24 * time.Hour))
		v["until"] = w.date(exp)
		v["days"] = w.days(max(0, left))
		v["term"] = fmt.Sprintf(w.termUntil, v["until"], v["days"])
	}
	used := u.UsedUp + u.UsedDown
	v["used"] = w.bytes(used)
	if u.TrafficLimit.Valid {
		v["limit"] = w.bytes(u.TrafficLimit.Int64)
		v["left"] = w.bytes(domain.TrafficLeft(u.TrafficLimit, used, extra))
		v["traffic"] = fmt.Sprintf(w.trafficOf, v["used"], w.withPackages(u.TrafficLimit.Int64, extra))
	} else {
		v["limit"], v["left"] = w.noLimit, w.noLimit
		v["traffic"] = fmt.Sprintf(w.trafficNoLimit, v["used"])
	}
	if t, ok := domain.NextReset(u, now); ok {
		v["reset"] = w.shortDate(t)
	}
	n, _ := b.d.Backend.DeviceCount(ctx, u.ID)
	v["devices"] = strconv.FormatInt(n, 10)
	switch {
	case u.DeviceLimit.Valid && !domain.BindsDevices(u.DeviceMode):
		// "3 of 3" would tell of places that this limit does not have: it counts the devices
		// online at once, whichever and however many are bound.
		v["devices"] = fmt.Sprintf(linkWordsFor(w).onlineLimit, strconv.FormatInt(u.DeviceLimit.Int64, 10))
	case u.DeviceLimit.Valid:
		v["devices"] = fmt.Sprintf(w.trafficOf, v["devices"], strconv.FormatInt(u.DeviceLimit.Int64, 10))
	}
	// The subscription link itself: an admin's own menu text can show it without the
	// "Connect" screen.
	v["link"] = b.subURL(ctx, u)
	return v
}

func (b *Bot) brand(ctx context.Context) string { return brandOf(ctx, b.d.Backend) }

// brandOf is the panel's name for the service as the bot says it.
func brandOf(ctx context.Context, be Backend) string {
	if s := be.Brand(ctx); s != "" {
		return s
	}
	return "VPN"
}

// supportURL is the panel's support link when Telegram can open it.
func (b *Bot) supportURL(ctx context.Context) string {
	s := b.d.Backend.SupportURL(ctx)
	if tgate.SafeURL(s) {
		return s
	}
	return ""
}

func (b *Bot) subURL(ctx context.Context, u db.User) string {
	return b.d.Backend.SubURL(ctx, u)
}

// poolLines: one line per traffic pool with a limit — "WL: 30 GB of 100 GB + packages 50 GB".
func (b *Bot) poolLines(ctx context.Context, w *words, userID int64) []string {
	pools, err := b.d.Backend.Pools(ctx, userID, b.d.Now())
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range pools {
		line := p.Name + ": " + fmt.Sprintf(w.trafficOf, w.bytes(p.Used), w.withPackages(p.Limit, p.Extra))
		if p.Exhausted {
			line += " — " + w.poolOut
		}
		out = append(out, line)
	}
	return out
}
