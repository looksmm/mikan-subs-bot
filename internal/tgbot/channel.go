package tgbot

import (
	"context"
	"errors"
	"fmt"
	"html"

	"subsbot/internal/billing"
	"subsbot/internal/tgate"
)

// The channel a subscriber must join before the bot shows its menu (Config.Channel), and
// the free plan, which may ask for the same channel. The gateway asks Telegram who is in
// it. Callback data: gc checks the channel again, fp takes the free plan.

// ungated are the screens the channel never stands in front of: paying, and renewing or
// buying traffic for a subscription the account has. A payment already started is never
// blocked, and nobody is kept from paying for what they hold.
var ungated = map[string]bool{"pn": true, "py": true, "tp": true, "t": true, "r": true, "x": true, "xk": true, "xp": true}

// passes: chat may see the menu. The gate is off, the account is in the channel, or the
// channel cannot be asked: then nobody is locked out for the panel's trouble, the log
// and the admin's page say what is wrong.
func (b *Bot) passes(ctx context.Context, cfg Config, chat int64, fresh bool) bool {
	if !cfg.Channel.Gate() {
		return true
	}
	in, err := b.d.Backend.Member(ctx, cfg.Channel.Chat, chat, fresh)
	return in || err != nil
}

// gateScreen stands in front of the menu: the admin's text, a button to the channel and
// one that checks again.
func (b *Bot) gateScreen(ctx context.Context, cfg Config, w *words, notice string) (string, *tgate.Keyboard) {
	text := render(pick(cfg.Texts.Channel, w.channel), map[string]string{"brand": b.brand(ctx)})
	if notice != "" {
		text = html.EscapeString(notice) + "\n\n" + text
	}
	return text, &tgate.Keyboard{InlineKeyboard: channelButtons(cfg, w, "gc")}
}

// channelButtons: the link to join the channel and a check that goes to data.
func channelButtons(cfg Config, w *words, data string) [][]tgate.Button {
	var rows [][]tgate.Button
	if link := cfg.Channel.Link(); link != "" {
		rows = append(rows, []tgate.Button{{Text: w.channelJoin, URL: link}})
	}
	return append(rows, []tgate.Button{{Text: w.channelCheck, CallbackData: data}})
}

// freeButton is the free plan's button, while the account may take it.
func (b *Bot) freeButton(ctx context.Context, cfg Config, w *words, chat int64) (tgate.Button, bool) {
	shop := b.d.Backend.Shop()
	if _, ok := shop.FreeOffer(ctx, chat); !ok {
		return tgate.Button{}, false
	}
	return tgate.Button{Text: pick(cfg.Texts.FreeButton, w.freeButton), CallbackData: "fp"}, true
}

// takeFree gives the chat the free plan and says what it got. An account the plan's
// channel does not see yet gets the channel's buttons, the check taking the plan again.
func (b *Bot) takeFree(ctx context.Context, cfg Config, w *words, chat int64) (string, *tgate.Keyboard) {
	home := &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.back, CallbackData: "m"}}}}
	shop := b.d.Backend.Shop()
	u, err := shop.FreePlan(ctx, chat)
	switch {
	case errors.Is(err, billing.ErrFreeChannel):
		return html.EscapeString(w.freeChannel), &tgate.Keyboard{InlineKeyboard: append(channelButtons(cfg, w, "fp"), home.InlineKeyboard...)}
	case errors.Is(err, billing.ErrFreeActive):
		return html.EscapeString(w.freeActive), home
	case errors.Is(err, billing.ErrFreeOff):
		return html.EscapeString(w.freeOff), home
	case errors.Is(err, billing.ErrTooManySubs):
		return html.EscapeString(fmt.Sprintf(w.linkLimit, tgate.MaxLinks)), home
	case err != nil:
		b.d.Log.Warn("telegram: free plan", "tg", chat, "err", err)
		return html.EscapeString(w.freeFail), home
	}
	what := u.Name
	if t, err := b.d.Backend.Tariff(ctx, u.TariffID.Int64); err == nil {
		what = billing.Describe(t, t.DurationDays, cfg.Lang)
	}
	return fmt.Sprintf(html.EscapeString(w.freeDone), html.EscapeString(what)), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.trialOpen, CallbackData: "m"}}}}
}
