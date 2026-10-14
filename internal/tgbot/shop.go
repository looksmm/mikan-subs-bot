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
	"subsbot/internal/events"
	"subsbot/internal/tgate"
)

// Callback data of the shop: b buy a new subscription, tn:<tariff> its tariff, pn:<tariff>:<p>
// pay for it; r renew the shown one, t:<tariff>, py:<tariff>:<p>. <tariff> is the tariff's
// id, or <id>.<days> for one of its terms (a tariff sold for several); the id alone is its
// first term, as on buttons sent before terms. <p> is s (Stars) or a-<id> (a marketplace
// adapter); y and c are YooKassa's and CryptoBot's, short, and as messages sent before
// 0.4.4 carry them for the built-in providers those adapters replaced. The longest,
// py:<19 digits>.<4 digits>:a-<32>, is 62 bytes: Telegram takes 64.

var providerCodes = map[string]string{"s": billing.Stars, "y": billing.AddonPrefix + "yookassa", "c": billing.AddonPrefix + "cryptobot"}

// addonCode is an adapter's code on a pay button.
func addonCode(id string) string {
	for code, p := range providerCodes {
		if p == billing.AddonPrefix+id {
			return code
		}
	}
	return "a-" + id
}

// providerOf is the provider a button's code names.
func providerOf(code string) (string, bool) {
	if p, ok := providerCodes[code]; ok {
		return p, true
	}
	id, ok := strings.CutPrefix(code, "a-")
	if !ok || billing.AddonID(billing.AddonPrefix+id) == "" {
		return "", false
	}
	return billing.AddonPrefix + id, true
}

// addonName names adapters on the buttons in the bot's language.
func (b *Bot) addonName(ctx context.Context) func(id string) string {
	lang := b.lang(ctx)
	shop := b.d.Backend.Shop()
	return func(id string) string { return shop.AddonName(ctx, id, lang) }
}

// offers lists what the chat can buy; nil without payments.
func (b *Bot) offers(ctx context.Context) ([]billing.Offer, billing.Available) {
	o, av, err := b.d.Backend.Shop().Offers(ctx)
	if err != nil {
		b.d.Log.Warn("telegram: offers", "err", err)
		return nil, av
	}
	return o, av
}

// canBuyNew: people without a subscription may buy one here.
func (b *Bot) canBuyNew(ctx context.Context) bool {
	if !b.d.Backend.Shop().Config(ctx).AllowNew {
		return false
	}
	o, _ := b.offers(ctx)
	return len(o) > 0
}

// price: "⭐ 150" or "199 ₽" / "199,50 ₽".
func (w *words) price(amount int64, currency string) string {
	if currency == "XTR" {
		return "⭐ " + strconv.FormatInt(amount, 10)
	}
	s := strconv.FormatInt(amount/100, 10)
	if k := amount % 100; k != 0 {
		sep := ","
		if w == &en {
			sep = "."
		}
		s += fmt.Sprintf("%s%02d", sep, k)
	}
	return s + " ₽"
}

// shopList: the tariffs on sale as buttons; prefix is "t" (renew) or "tn" (new).
func (b *Bot) shopList(ctx context.Context, w *words, title, prefix, notice string, back []tgate.Button) (string, *tgate.Keyboard) {
	offers, _ := b.offers(ctx)
	lang := b.lang(ctx)
	lines := []string{"<b>" + html.EscapeString(title) + "</b>", ""}
	if notice != "" {
		lines = append([]string{html.EscapeString(notice), ""}, lines...)
	}
	rows := [][]tgate.Button{}
	for _, o := range offers {
		desc := billing.DescribeOffer(o, lang)
		lines = append(lines, "• <b>"+html.EscapeString(o.Tariff.Name)+"</b> — "+html.EscapeString(desc)+noteOf(o.Tariff.Description))
		rows = append(rows, []tgate.Button{{Text: o.Tariff.Name + " · " + w.cheapest(o), CallbackData: prefix + ":" + strconv.FormatInt(o.Tariff.ID, 10)}})
	}
	if len(offers) == 0 {
		lines = append(lines, html.EscapeString(w.payUnavailable))
	}
	// plus: the admin's templates of this screen.
	if text, ok := b.shopListText(ctx, w, title, notice, offers); ok {
		return text, &tgate.Keyboard{InlineKeyboard: append(rows, back)}
	}
	return strings.Join(lines, "\n"), &tgate.Keyboard{InlineKeyboard: append(rows, back)}
}

// noteOf is the admin's description of a tariff to follow the line built from its limits:
// on lines of its own, escaped for the HTML parse mode, its line breaks kept. Empty without
// a description, so a tariff that has none looks as it did.
func noteOf(description string) string {
	if description == "" {
		return ""
	}
	return "\n" + html.EscapeString(description)
}

// cheapest is the price a list shows: rubles when sold for them, else Stars; "from" the
// cheapest term of a tariff sold for several.
func (w *words) cheapest(o billing.Offer) string {
	terms := o.Terms
	if len(terms) == 0 {
		terms = []billing.OfferTerm{{Stars: o.Stars, Rub: o.Rub}}
	}
	var rub, stars int64
	for _, t := range terms {
		if t.Rub > 0 && (rub == 0 || t.Rub < rub) {
			rub = t.Rub
		}
		if t.Stars > 0 && (stars == 0 || t.Stars < stars) {
			stars = t.Stars
		}
	}
	price := w.price(stars, "XTR")
	if rub > 0 {
		price = w.price(rub, "RUB")
	}
	if len(terms) > 1 {
		return fmt.Sprintf(w.priceFrom, price)
	}
	return price
}

// tariffArg reads <id> or <id>.<days> from callback data; days is nil without them.
func tariffArg(s string) (id int64, days *int64) {
	idStr, daysStr, found := strings.Cut(s, ".")
	id, _ = strconv.ParseInt(idStr, 10, 64)
	if found {
		if d, err := strconv.ParseInt(daysStr, 10, 64); err == nil && d >= 0 {
			days = &d
		}
	}
	return id, days
}

// shopTariff: one tariff and a button per way to pay. A tariff sold for several terms
// shows its terms first, a button each. arg is <id> or <id>.<days>; prefix is the screen's
// ("tn" new, "t" renew), pay the pay buttons' ("pn", "py").
func (b *Bot) shopTariff(ctx context.Context, w *words, arg, prefix, pay string, back []tgate.Button) (string, *tgate.Keyboard) {
	id, days := tariffArg(arg)
	offers, av := b.offers(ctx)
	lang := b.lang(ctx)
	for _, o := range offers {
		if o.Tariff.ID != id {
			continue
		}
		name := "<b>" + html.EscapeString(o.Tariff.Name) + "</b>\n"
		ref := strconv.FormatInt(id, 10)
		if days == nil && len(o.Terms) == 1 && o.Terms[0].Days != o.First {
			// The first term is not sold now (no provider takes its price): the buttons name
			// the one that is, as an invoice without days would be for the first.
			only := o.Terms[0].Days
			days = &only
		}
		if days == nil && len(o.Terms) > 1 {
			rows := [][]tgate.Button{}
			for _, t := range o.Terms {
				label := billing.TermLabel(o.Tariff, t.Days, lang) + " · " + w.cheapest(billing.Offer{Stars: t.Stars, Rub: t.Rub})
				rows = append(rows, []tgate.Button{{Text: label, CallbackData: prefix + ":" + ref + "." + strconv.FormatInt(t.Days, 10)}})
			}
			if text, ok := b.tariffText(ctx, w, o, nil, w.pickTerm); ok { // plus: the admin's template
				return text, &tgate.Keyboard{InlineKeyboard: append(rows, back)}
			}
			return name + html.EscapeString(billing.DescribeLimits(o.Tariff, lang)) + noteOf(o.Tariff.Description) + "\n\n" + w.pickTerm, &tgate.Keyboard{InlineKeyboard: append(rows, back)}
		}
		t, ok := o.Term(days)
		if !ok {
			break
		}
		if days != nil {
			ref += "." + strconv.FormatInt(t.Days, 10)
		}
		text := name + html.EscapeString(billing.Describe(o.Tariff, t.Days, lang)) + noteOf(o.Tariff.Description) + "\n\n" + w.payHow
		rows := w.payButtons(pay+":"+ref+":", t.Stars, t.Rub, av, b.addonName(ctx))
		if tt, ok := b.tariffText(ctx, w, o, &t, w.payHow); ok { // plus: the admin's template
			text = tt
		}
		return text, &tgate.Keyboard{InlineKeyboard: append(rows, back)}
	}
	return html.EscapeString(w.notForSale), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{back}}
}

// shopBack is where the back button of a tariff screen leads: the term list of a tariff
// sold for several when a term is shown, else the list of tariffs.
func shopBack(w *words, prefix, arg, list string) []tgate.Button {
	if id, _, found := strings.Cut(arg, "."); found {
		return []tgate.Button{{Text: w.back, CallbackData: prefix + ":" + id}}
	}
	return []tgate.Button{{Text: w.back, CallbackData: list}}
}

// shopInvoice opens the invoice and shows its pay button. It runs when the chat's screen
// is drawn, off the update loop: the provider may take seconds to answer.
func (b *Bot) shopInvoice(ctx context.Context, w *words, chat, userID int64, arg string, back []tgate.Button) (string, *tgate.Keyboard) {
	ref, code, _ := strings.Cut(arg, ":")
	id, days := tariffArg(ref)
	provider, ok := providerOf(code)
	shop := b.d.Backend.Shop()
	if !ok {
		return html.EscapeString(w.payUnavailable), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{back}}
	}
	p, err := shop.Invoice(ctx, billing.InvoiceRequest{TgID: chat, UserID: userID, TariffID: id, TermDays: days, Provider: provider})
	if err != nil {
		return html.EscapeString(w.payError(err)), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{back}}
	}
	done := w.payNew
	if userID != 0 {
		done = w.payRenew
	}
	name := p.TariffName
	if days != nil && p.TermDays.Valid {
		// A tariff sold for several terms: the invoice says which one.
		if t, err := b.d.Backend.Tariff(ctx, id); err == nil {
			name += " · " + billing.TermLabel(t, p.TermDays.Int64, b.lang(ctx))
		}
	}
	text := fmt.Sprintf(w.invoice, html.EscapeString(name), html.EscapeString(w.price(p.Amount, p.Currency)), done)
	return text, &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: fmt.Sprintf(w.payButton, w.price(p.Amount, p.Currency)), URL: p.PayUrl}}, back}}
}

func (w *words) payError(err error) string {
	switch {
	case errors.Is(err, billing.ErrNotForSale):
		return w.notForSale
	case errors.Is(err, billing.ErrTooMany):
		return w.tooManyInvoices
	case errors.Is(err, billing.ErrTooManySubs):
		return fmt.Sprintf(w.linkLimit, tgate.MaxLinks)
	case errors.Is(err, billing.ErrNotYours):
		return w.noSub
	}
	return w.payUnavailable
}

func (b *Bot) lang(ctx context.Context) string { return b.config(ctx).Lang }

// paid is what payment.paid says: the buyer learns the subscription is ready (with the
// link for a new one) or the traffic package is added, and gets a fresh menu that shows it.
func (b *Bot) paid(ctx context.Context, chat int64, p events.Payment, u db.User) {
	w := wordsFor(b.lang(ctx))
	var text string
	switch {
	case p.Kind == billing.KindPackage:
		text = fmt.Sprintf(w.paidPackage, html.EscapeString(p.TariffName), html.EscapeString(u.Name))
	case p.Created:
		text = fmt.Sprintf(w.paidNew, html.EscapeString(u.Name), html.EscapeString(p.TariffName), html.EscapeString(b.subURL(ctx, u)))
	default:
		until := w.forever
		if u.ExpiresAt.Valid {
			until = w.date(time.Unix(u.ExpiresAt.Int64, 0).UTC())
		}
		text = fmt.Sprintf(w.paidRenew, html.EscapeString(u.Name), html.EscapeString(until))
	}
	_ = b.d.Backend.SetCurrent(ctx, chat, u.ID)
	b.d.Outbox.Notice(chat, func(ctx context.Context, c *tgate.Client) error {
		_, err := c.Send(ctx, chat, text, nil, false)
		return err
	}, nil)
	b.freshMenu(chat, "")
}

// refunded is what payment.refunded says: the buyer learns the payment was refunded and what
// it gave is taken back.
func (b *Bot) refunded(ctx context.Context, chat int64, p events.Payment, u db.User) {
	w := wordsFor(b.lang(ctx))
	var text string
	switch {
	case u.ID == 0:
		text = fmt.Sprintf(w.refundedGone, html.EscapeString(p.TariffName))
	case p.Kind == billing.KindPackage:
		text = fmt.Sprintf(w.refundedPackage, html.EscapeString(p.TariffName), html.EscapeString(u.Name))
	case p.Disabled:
		text = fmt.Sprintf(w.refundedNew, html.EscapeString(p.TariffName), html.EscapeString(u.Name))
	default:
		until := w.forever
		if u.ExpiresAt.Valid {
			until = w.date(time.Unix(u.ExpiresAt.Int64, 0).UTC())
		}
		text = fmt.Sprintf(w.refundedRenew, html.EscapeString(p.TariffName), html.EscapeString(u.Name), html.EscapeString(until))
	}
	b.d.Outbox.Notice(chat, func(ctx context.Context, c *tgate.Client) error {
		_, err := c.Send(ctx, chat, text, nil, false)
		return err
	}, nil)
	b.freshMenu(chat, "")
}

// takeTrial gives the chat its free trial and says what it got, or shows the ways to pay
// for a priced one; a chat that had one (or is a customer) is told why not.
func (b *Bot) takeTrial(ctx context.Context, w *words, chat int64) (string, *tgate.Keyboard) {
	home := &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.back, CallbackData: "m"}}}}
	shop := b.d.Backend.Shop()
	if shop.Config(ctx).TrialPaid() {
		return b.trialPay(ctx, w, chat)
	}
	u, err := shop.Trial(ctx, chat)
	switch {
	case errors.Is(err, billing.ErrTrialUsed):
		return html.EscapeString(w.trialUsed), home
	case errors.Is(err, billing.ErrTrialOff):
		return html.EscapeString(w.trialOff), home
	case err != nil:
		b.d.Log.Warn("telegram: trial", "tg", chat, "err", err)
		return html.EscapeString(w.trialFail), home
	}
	what := u.Name
	if t, err := b.d.Backend.Tariff(ctx, u.TariffID.Int64); err == nil {
		what = billing.Describe(t, t.DurationDays, b.lang(ctx))
	}
	return fmt.Sprintf(html.EscapeString(w.trialDone), html.EscapeString(what)), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.trialOpen, CallbackData: "m"}}}}
}

// trialPay shows what the priced trial gives and a button for each way to pay for it.
func (b *Bot) trialPay(ctx context.Context, w *words, chat int64) (string, *tgate.Keyboard) {
	home := []tgate.Button{{Text: w.back, CallbackData: "m"}}
	shop := b.d.Backend.Shop()
	o, av, ok := shop.TrialOffer(ctx, chat)
	if !ok {
		why := w.trialOff
		if _, on := shop.TrialTariff(ctx); on {
			// Offered, but not to this account, or for a price nobody takes now.
			why = w.payUnavailable
			if !shop.TrialAllowed(ctx, chat) {
				why = w.trialUsed
			}
		}
		return html.EscapeString(why), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{home}}
	}
	text := "<b>" + html.EscapeString(w.trialTitle) + "</b>\n" + html.EscapeString(billing.Describe(o.Tariff, o.Tariff.DurationDays, b.lang(ctx))) + "\n\n" + w.payHow
	rows := w.payButtons("tp:", o.Stars, o.Rub, av, b.addonName(ctx))
	return text, &tgate.Keyboard{InlineKeyboard: append(rows, home)}
}

// trialInvoice opens the invoice for the priced trial with the provider a button names.
func (b *Bot) trialInvoice(ctx context.Context, w *words, chat int64, code string) (string, *tgate.Keyboard) {
	back := []tgate.Button{{Text: w.back, CallbackData: "tr"}}
	provider, ok := providerOf(code)
	shop := b.d.Backend.Shop()
	if !ok {
		return html.EscapeString(w.payUnavailable), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{back}}
	}
	p, err := shop.TrialInvoice(ctx, chat, provider)
	switch {
	case errors.Is(err, billing.ErrTrialUsed):
		return html.EscapeString(w.trialUsed), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.back, CallbackData: "m"}}}}
	case errors.Is(err, billing.ErrTrialOff):
		return html.EscapeString(w.trialOff), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.back, CallbackData: "m"}}}}
	case err != nil:
		return html.EscapeString(w.payError(err)), &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{back}}
	}
	text := fmt.Sprintf(w.invoice, html.EscapeString(w.trialTitle+" · "+p.TariffName), html.EscapeString(w.price(p.Amount, p.Currency)), w.payNew)
	return text, &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: fmt.Sprintf(w.payButton, w.price(p.Amount, p.Currency)), URL: p.PayUrl}}, back}}
}

// trialNotGiven is what trial.not_given says: it tells the buyer a paid trial was not given,
// as the account had its trial, and that its money comes back through support (the payment
// method cannot refund it), with the support link when the panel has one.
func (b *Bot) trialNotGiven(ctx context.Context, chat int64, n events.NotGiven) {
	w := wordsFor(b.lang(ctx))
	text := fmt.Sprintf(w.trialNotGiven, html.EscapeString(w.price(n.Amount, n.Currency)))
	var kb *tgate.Keyboard
	if sup := b.supportURL(ctx); sup != "" {
		kb = &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.support, URL: sup}}}}
	}
	b.d.Outbox.Notice(chat, func(ctx context.Context, c *tgate.Client) error {
		_, err := c.Send(ctx, chat, text, kb, false)
		return err
	}, nil)
}
