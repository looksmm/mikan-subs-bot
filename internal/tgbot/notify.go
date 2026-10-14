package tgbot

import (
	"context"
	"strconv"
	"time"

	"subsbot/internal/db"
	"subsbot/internal/events"
	"subsbot/internal/tgate"
)

// night: automatic notices then come without a sound. Most users live on Moscow time;
// a notice at 3 a.m. with a ring is how a bot gets reported as spam.
func night(t time.Time) bool {
	h := t.In(time.FixedZone("MSK", 3*60*60)).Hour()
	return h >= 22 || h < 9
}

// noticeKind is the kind of notice an event names; days is the user.expiring event's.
func noticeKind(typ string, days int) string {
	switch typ {
	case events.UserExpired:
		return "expired"
	case events.Traffic90:
		return "traffic_90"
	case events.Traffic100:
		return "traffic_100"
	}
	if days == 1 {
		return "expire_1d"
	}
	return "expire_3d"
}

// noticeMessage is a notice of kind about u, as the admin worded it, with its buttons.
func (b *Bot) noticeMessage(ctx context.Context, cfg Config, w *words, u db.User, kind string, now time.Time) (string, *tgate.Keyboard) {
	var tpl string
	switch kind {
	case "expire_3d", "expire_1d":
		tpl = pick(cfg.Texts.Expiring, w.expiring)
	case "expired":
		tpl = pick(cfg.Texts.Expired, w.expired)
	case "traffic_90":
		tpl = pick(cfg.Texts.Traffic90, w.traffic90)
	case "traffic_100":
		tpl = pick(cfg.Texts.TrafficEnd, w.trafficEnd)
	}
	text := render(tpl, b.vars(ctx, w, u, now))
	var rows [][]tgate.Button
	// Traffic running out: the packages there are for this subscription, a tap away.
	if kind == "traffic_90" || kind == "traffic_100" {
		if len(b.packageOffers(ctx, u.ID)) > 0 {
			rows = append(rows, []tgate.Button{{Text: w.buyTraffic, CallbackData: "xu:" + strconv.FormatInt(u.ID, 10)}})
		}
	}
	if kind != "traffic_90" {
		rows = append(rows, []tgate.Button{{Text: labelOf(cfg, "renew", "💳"), CallbackData: "r"}})
	}
	var kb *tgate.Keyboard
	if len(rows) > 0 {
		kb = &tgate.Keyboard{InlineKeyboard: rows}
	}
	return text, kb
}
