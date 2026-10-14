package tgbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"

	"subsbot/internal/db"
	"subsbot/internal/domain"
	"subsbot/internal/events"
	"subsbot/internal/tgate"
)

// The screens run as the bot addon and the panel tells them what the buyer and the
// subscriber must hear through its feed of events: the payment messages (events.PaymentPaid,
// PaymentRefunded and TrialNotGiven, written by billing in the transaction of the change) and
// the term and traffic notices (Panel.Notify). HandleEvent is the addon's side: it takes the
// subscription over the Panel API, as any addon would.

// PaymentMessage is what the bot.* events carry: the whole payment and subscription.
//
// The bot.* events are deprecated: Notifications keeps writing them for one release so the
// bot builds released before the payment events carried what the bot needs keep working. This
// build does not read them (EventTypes), so no message comes twice.
type PaymentMessage struct {
	Payment  db.Payment `json:"payment"`
	User     db.User    `json:"user"`
	Created  bool       `json:"created,omitempty"`  // bot.paid: the payment made the subscription
	Disabled bool       `json:"disabled,omitempty"` // bot.refunded: the subscription was turned off
}

// Emit writes an event of the panel's feed; dedupe makes a second emit of it a no-op.
type Emit func(ctx context.Context, typ string, userID, tgID int64, data any, dedupe string) error

// Notifications is billing's Notifier as the deprecated bot.* events (see PaymentMessage).
// They are written after the commit and can be lost on a crash; events.PaymentPaid and its
// kin are not.
type Notifications struct {
	Emit Emit
	Log  *slog.Logger
}

func (n Notifications) emit(ctx context.Context, typ string, m PaymentMessage) {
	// Told once per payment and kind, whoever calls again (a webhook and Telegram's update).
	if err := n.Emit(ctx, typ, m.User.ID, m.Payment.TgID, m, typ+":"+strconv.FormatInt(m.Payment.ID, 10)); err != nil {
		n.Log.Error("telegram: payment message event", "payment", m.Payment.ID, "err", err)
	}
}

func (n Notifications) Paid(ctx context.Context, p db.Payment, u db.User, created bool) {
	n.emit(ctx, events.BotPaid, PaymentMessage{Payment: p, User: u, Created: created})
}

func (n Notifications) Refunded(ctx context.Context, p db.Payment, u db.User, disabled bool) {
	n.emit(ctx, events.BotRefunded, PaymentMessage{Payment: p, User: u, Disabled: disabled})
}

func (n Notifications) TrialNotGiven(ctx context.Context, p db.Payment) {
	n.emit(ctx, events.BotTrialNotGiven, PaymentMessage{Payment: p})
}

// EventTypes are the events HandleEvent acts on.
var EventTypes = []string{events.PaymentPaid, events.PaymentRefunded, events.TrialNotGiven,
	events.UserExpiring, events.UserExpired, events.Traffic90, events.Traffic100}

// gone: the subscription of a payment is not there for the bot any more: deleted, or moved to
// an account that is not talking to it. Retrying would not change that.
func gone(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, ErrNotTalking)
}

// HandleEvent sends what an event of the panel asks the bot to say. An error leaves the
// event unacknowledged, to come again.
func (b *Bot) HandleEvent(ctx context.Context, e events.Event) error {
	switch e.Type {
	case events.PaymentPaid, events.PaymentRefunded:
		var p events.Payment
		if err := json.Unmarshal(e.Data, &p); err != nil || e.TgID == 0 {
			return nil // nothing to say from it, now or later
		}
		var u db.User
		if e.UserID != 0 {
			var err error
			if u, err = b.d.Backend.User(ctx, e.UserID); err != nil {
				if !gone(err) {
					return err
				}
				u = db.User{} // deleted since: a refund still says the money is back
			}
		}
		if e.Type == events.PaymentPaid {
			if u.ID == 0 {
				return nil // a subscription that is gone has nothing to be thanked for
			}
			b.paid(ctx, e.TgID, p, u)
			b.autoEvent(ctx, e, u) // plus: the admin's message after a payment
		} else {
			b.refunded(ctx, e.TgID, p, u)
		}
	case events.TrialNotGiven:
		var n events.NotGiven
		if err := json.Unmarshal(e.Data, &n); err != nil || e.TgID == 0 {
			return nil
		}
		// A trial that went back by itself is told by its PaymentRefunded; only the one the
		// admin must return by hand has no other word.
		if n.Refund == events.RefundedManually {
			b.trialNotGiven(ctx, e.TgID, n)
		}
	case events.UserExpiring, events.UserExpired, events.Traffic90, events.Traffic100:
		if e.TgID == 0 {
			return nil
		}
		var n events.Notice
		_ = json.Unmarshal(e.Data, &n)
		kind := noticeKind(e.Type, n.Days)
		u, err := b.d.Backend.User(ctx, e.UserID)
		if err != nil {
			return err
		}
		if b.autoEvent(ctx, e, u) { // plus: the admin's message in place of the panel's
			return nil
		}
		cfg := b.config(ctx)
		now := b.d.Now()
		if b.ownBuiltin(ctx, kind, e.TgID, b.noticeVars(ctx, wordsFor(cfg.Lang), e.TgID, u), true) { // plus
			return nil
		}
		text, kb := b.noticeMessage(ctx, cfg, wordsFor(cfg.Lang), u, kind, now)
		silent, chat := cfg.QuietNight && night(now), e.TgID
		b.d.Outbox.Notice(chat, func(ctx context.Context, c *tgate.Client) error {
			_, err := c.Send(ctx, chat, text, kb, silent)
			return err
		}, b.countBuiltin(kind))
	}
	return nil
}
