package tgbot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"time"

	"subsbot/internal/db"
	"subsbot/internal/domain"
	"subsbot/internal/tgate"
)

// The subscriber's own new link: a button on the subscription screen, a confirmation that
// says what stops working, and the new link right after. Callback data: rl confirm, rn:<user>
// reissue (the id goes with the button, as in rd, so that a tap after switching
// subscriptions reissues the one that was asked about).

// linkWords are the bot's words about the new link and about the online device mode. They
// live apart from words so that a text of the main table is not edited for them.
type linkWords struct {
	button, confirm, yes, done, off, failed string
	// wait is the line over the subscription screen once the limit is reached.
	wait string
	// onlineLimit replaces "N of M" for a user whose limit counts devices online at once;
	// onlineNote tells the same on the devices screen.
	onlineLimit, onlineNote string
}

var linkRu = linkWords{
	button:      "🔄 Новая ссылка",
	confirm:     "Выпустить новую ссылку для «%s»?\n\nСтарая ссылка перестанет работать сразу, а все привязанные устройства отключатся. Приложения придётся подписать заново: добавьте в них новую ссылку.",
	yes:         "✅ Да, выпустить",
	done:        "✅ Новая ссылка готова. Старая больше не работает: добавьте новую в приложения.",
	off:         "Сменить ссылку может только администратор: напишите в поддержку.",
	failed:      "⚠️ Не получилось выпустить новую ссылку. Попробуйте ещё раз чуть позже.",
	wait:        "🔄 Новая ссылка: можно снова через %s",
	onlineLimit: "до %s одновременно",
	onlineNote:  "Лимит считает только устройства, подключённые одновременно: добавить можно сколько угодно.",
}

var linkEn = linkWords{
	button:      "🔄 New link",
	confirm:     "Issue a new link for “%s”?\n\nThe old link stops working at once and all bound devices are disconnected. The apps need the new link: add it to each of them.",
	yes:         "✅ Yes, issue it",
	done:        "✅ The new link is ready. The old one no longer works: add the new one to your apps.",
	off:         "Only the administrator can change the link: write to support.",
	failed:      "⚠️ The new link was not issued. Please try again a little later.",
	wait:        "🔄 New link: available again in %s",
	onlineLimit: "up to %s at once",
	onlineNote:  "The limit counts only the devices connected at once: you can add as many as you like.",
}

func linkWordsFor(w *words) *linkWords {
	if w == &en {
		return &linkEn
	}
	return &linkRu
}

// in is how long until t, to the next whole hour (days when it is whole days): "5 ч".
func (b *Bot) in(w *words, t time.Time) string {
	hours := int((t.Sub(b.d.Now()) + time.Hour - time.Nanosecond) / time.Hour)
	return w.span(max(hours, 1))
}

// reissueState is what the subscriber may do about the link now; ok is false when the bot
// cannot reissue at all.
func (b *Bot) reissueState(ctx context.Context, u db.User) (domain.ReissueState, bool) {
	st, err := b.d.Backend.ReissueState(ctx, u.ID)
	if err != nil {
		b.d.Log.Warn("telegram: reissue rules", "err", err)
		return st, false
	}
	return st, true
}

// reissueRow is the subscription screen's part about the link: the button while the rules
// allow it, and, while the limit is reached, the line that says when it is back. Nothing
// when only the admin reissues.
func (b *Bot) reissueRow(ctx context.Context, w *words, u db.User) (button *tgate.Button, line string) {
	st, ok := b.reissueState(ctx, u)
	if !ok || st.Rules.Off {
		return nil, ""
	}
	lw := linkWordsFor(w)
	if !st.Next.IsZero() {
		return nil, html.EscapeString(fmt.Sprintf(lw.wait, b.in(w, st.Next)))
	}
	return &tgate.Button{Text: lw.button, CallbackData: "rl"}, ""
}

// reissueConfirm is "rl": what the new link costs, before it is done. The screen of a
// subscription that may not reissue now is the subscription screen with the reason.
func (b *Bot) reissueConfirm(ctx context.Context, w *words, u db.User) (string, *tgate.Keyboard, bool) {
	lw := linkWordsFor(w)
	st, ok := b.reissueState(ctx, u)
	switch {
	case !ok:
		return "", nil, false
	case st.Rules.Off:
		return lw.off, nil, false
	case !st.Next.IsZero():
		return fmt.Sprintf(lw.wait, b.in(w, st.Next)), nil, false
	}
	return html.EscapeString(fmt.Sprintf(lw.confirm, u.Name)), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{
		{{Text: lw.yes, CallbackData: "rn:" + strconv.FormatInt(u.ID, 10)}, {Text: w.cancel, CallbackData: "s"}}}}, true
}

// reissueAct is the tap "rn:<user>": a new link for that subscription of the account, and
// the connect screen with it. Only a subscription of the account is touched.
func (b *Bot) reissueAct(ctx context.Context, chat, id int64) (screen, notice string) {
	w := wordsFor(b.config(ctx).Lang)
	lw := linkWordsFor(w)
	list, _, _ := b.subs(ctx, chat)
	for _, s := range list {
		if s.ID != id {
			continue
		}
		err := b.d.Backend.Reissue(ctx, id)
		switch {
		case err == nil:
			// The connect screen shows the current subscription: the one just reissued.
			_ = b.d.Backend.SetCurrent(ctx, chat, id)
			b.d.Log.Info("telegram: the subscriber reissued their link", "user", id)
			return "c", lw.done
		case errors.Is(err, domain.ErrReissueCooldown):
			st, _ := b.d.Backend.ReissueState(ctx, id)
			return "s", fmt.Sprintf(lw.wait, b.in(w, st.Next))
		case errors.Is(err, domain.ErrReissueOff):
			return "s", lw.off
		case errors.Is(err, domain.ErrNotFound):
			return "m", ""
		default:
			b.d.Log.Warn("telegram: link not reissued", "user", id, "err", err)
			return "s", lw.failed
		}
	}
	return "m", ""
}
