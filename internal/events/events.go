// Code copied from mikan (events) by tools/extract: the declarations the bot needs.

package events

import (
	"encoding/json"
	"time"
)

// Event is one entry of the feed.
type Event struct {
	ID     int64           `json:"id"`
	Type   string          `json:"type"`
	At     time.Time       `json:"at"`
	UserID int64           `json:"user_id,omitempty"`
	TgID   int64           `json:"tg_id,omitempty"`
	Data   json.RawMessage `json:"data"`
}

// Types of events.
const (
	PaymentPaid     = "payment.paid"
	PaymentRefunded = "payment.refunded"

	UserCreated  = "user.created"
	UserGranted  = "user.granted"
	UserExpiring = "user.expiring"
	UserExpired  = "user.expired"
	Traffic90    = "traffic.90"
	Traffic100   = "traffic.100"

	TrialStarted  = "trial.started"
	TrialNotGiven = "trial.not_given"

	DeviceBound   = "device.bound"
	DeviceUnbound = "device.unbound"

	NodeDown = "node.down"
	NodeUp   = "node.up"
)

// The bot's own messages (deprecated), written after the commit by tgbot.Notifications and
// carrying a whole payment and a whole subscription (so only the addon that holds the bot
// reads them). They stay for one release so the bot builds released before PaymentPaid,
// PaymentRefunded and TrialNotGiven carried what the bot needs keep working; the bot addon
// built with this panel does not read them.
const (
	BotPaid          = "bot.paid"
	BotRefunded      = "bot.refunded"
	BotTrialNotGiven = "bot.trial_not_given"
)

// Payment is the data of PaymentPaid and PaymentRefunded: enough to thank, notify or count,
// nothing of the provider's secrets. The envelope's user_id is the subscription the payment
// gave to (0 on a refund when it is gone), tg_id the buyer.
type Payment struct {
	PaymentID  int64  `json:"payment_id"`
	Kind       string `json:"kind"`     // new | renew | package | trial
	Provider   string `json:"provider"` // stars | addon:<id> | ...
	Amount     int64  `json:"amount"`   // minor units
	Currency   string `json:"currency"`
	TariffID   int64  `json:"tariff_id,omitempty"`
	TariffName string `json:"tariff_name,omitempty"` // as it was when the buyer paid
	PackageID  int64  `json:"package_id,omitempty"`
	TermDays   int64  `json:"term_days,omitempty"`
	Created    bool   `json:"created,omitempty"`  // PaymentPaid: the payment made the subscription
	Disabled   bool   `json:"disabled,omitempty"` // PaymentRefunded: the subscription was turned off
}

// Notice is the data of UserExpiring, UserExpired, Traffic90 and Traffic100: which
// subscription (the envelope's user_id, tg_id the account it is linked to) and what is due.
type Notice struct {
	Days       int   `json:"days,omitempty"`        // UserExpiring: 3 or 1
	ExpiresAt  int64 `json:"expires_at,omitempty"`  // unix seconds
	PeriodFrom int64 `json:"period_from,omitempty"` // traffic: the period it is about
}

// Reasons and refunds of TrialNotGiven.
const (
	TrialUsed        = "trial_used" // the account had its trial or is a customer
	RefundedAuto     = "auto"       // the money went back; PaymentRefunded says so too
	RefundedManually = "manual"     // the payment method cannot refund: the admin does it by hand
)

// NotGiven is the data of TrialNotGiven: a trial an account paid for that the panel may no
// longer give. The envelope's tg_id is the buyer, user_id 0.
type NotGiven struct {
	PaymentID int64  `json:"payment_id"`
	Provider  string `json:"provider"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	TariffID  int64  `json:"tariff_id,omitempty"`
	TermDays  int64  `json:"term_days,omitempty"`
	Reason    string `json:"reason"` // TrialUsed
	Refund    string `json:"refund"` // RefundedAuto | RefundedManually
}
