package tgbot

import (
	"context"
	"time"

	"subsbot/internal/billing"
	"subsbot/internal/db"
	"subsbot/internal/events"
	"subsbot/internal/plus"
)

// The bot's own messages (a payment, a refund, the panel's reminders) as the admin words them
// on the page (plus.Auto.Builtin): the standard one, the admin's own text with buttons, or
// none at all. Each of them is counted under "m.<key>".

// builtinOf is what the admin set for the bot's own message of key.
func (b *Bot) builtinOf(key string) plus.Builtin {
	if b.x == nil || b.x.e.Auto == nil {
		return plus.Builtin{}
	}
	return b.x.e.Auto.Get().Builtin[key]
}

// ownBuiltin sends the admin's own message of key to chat, or nothing when the admin turned it
// off; false: the standard one is to go.
func (b *Bot) ownBuiltin(ctx context.Context, key string, chat int64, vars map[string]string, withSub bool) bool {
	bi := b.builtinOf(key)
	switch bi.Mode {
	case "off":
		return true
	case "own":
		pm := b.prepareMsg(ctx, bi.Message, "m."+key)
		if vars["brand"] == "" {
			vars["brand"] = pm.brand
		}
		b.queueAdmin(chat, pm, vars, withSub, "notice", nil)
		return true
	}
	return false
}

// countBuiltin counts a standard message of key that went out.
func (b *Bot) countBuiltin(key string) func(error) {
	return func(err error) {
		if err == nil && b.x != nil && b.x.e.Stats != nil {
			b.x.e.Stats.Sent("m." + key)
		}
	}
}

func paidKey(p events.Payment) string {
	switch {
	case p.Kind == billing.KindPackage:
		return "paid_package"
	case p.Created:
		return "paid_new"
	}
	return "paid_renew"
}

func refundKey(p events.Payment, u db.User) string {
	switch {
	case u.ID == 0:
		return "refunded_gone"
	case p.Kind == billing.KindPackage:
		return "refunded_package"
	case p.Disabled:
		return "refunded_new"
	}
	return "refunded_renew"
}

// payVars are the variables of a payment's or a refund's message.
func (b *Bot) payVars(ctx context.Context, w *words, chat int64, p events.Payment, u db.User) map[string]string {
	v := map[string]string{"name": u.Name, "tariff": p.TariffName, "amount": w.price(p.Amount, p.Currency),
		"until": w.forever, "days": "—", "first_name": b.firstName(chat)}
	if u.ExpiresAt.Valid {
		exp := time.Unix(u.ExpiresAt.Int64, 0).UTC()
		v["until"] = w.date(exp)
		v["days"] = w.days(max(0, int((exp.Sub(b.d.Now())+24*time.Hour-time.Second)/(24*time.Hour))))
	}
	if u.ID != 0 {
		v["link"] = b.subURL(ctx, u)
	}
	return v
}

// noticeVars are the variables of a reminder about u: the subscription's, its tariff, the name.
func (b *Bot) noticeVars(ctx context.Context, w *words, chat int64, u db.User) map[string]string {
	v := b.vars(ctx, w, u, b.d.Now())
	v["tariff"] = b.tariffName(ctx, u.TariffID.Int64, u.TariffID.Valid)
	v["first_name"] = b.firstName(chat)
	return v
}

// tariffName is a tariff's name, "" when there is none or the panel does not say.
func (b *Bot) tariffName(ctx context.Context, id int64, ok bool) string {
	if !ok {
		return ""
	}
	t, err := b.d.Backend.Tariff(ctx, id)
	if err != nil {
		return ""
	}
	return t.Name
}

// BuiltinDefaults are the standard texts of the bot's own messages, for the page to show next
// to the admin's: the words of the bot (the admin's texts over them) and the panel's reminders.
func (b *Bot) BuiltinDefaults(ctx context.Context) map[string]string {
	cfg := b.config(ctx)
	w := wordsFor(cfg.Lang)
	return map[string]string{
		"paid_new": w.paidNew, "paid_renew": w.paidRenew, "paid_package": w.paidPackage,
		"refunded_new": w.refundedNew, "refunded_renew": w.refundedRenew, "refunded_package": w.refundedPackage,
		"refunded_gone": w.refundedGone, "trial_not_given": w.trialNotGiven,
		"expire_3d": pick(cfg.Texts.Expiring, w.expiring), "expire_1d": pick(cfg.Texts.Expiring, w.expiring),
		"expired": pick(cfg.Texts.Expired, w.expired), "traffic_90": pick(cfg.Texts.Traffic90, w.traffic90),
		"traffic_100": pick(cfg.Texts.TrafficEnd, w.trafficEnd),
	}
}

// PanelNotify is which reminders the panel sends (Настроить → Напоминания клиентам), for the
// page to warn of two messages about one thing.
func (b *Bot) PanelNotify(ctx context.Context) map[string]bool {
	n := b.config(ctx).Notify
	return map[string]bool{"expire_3d": n.Expire3d, "expire_1d": n.Expire1d, "expired": n.Expired, "traffic_90": n.Traffic90, "traffic_100": n.Traffic100}
}
