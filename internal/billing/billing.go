// Code copied from mikan (billing) by tools/extract: the declarations the bot needs.

package billing

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"subsbot/internal/db"
	"subsbot/internal/domain"
)

// AddonPrefix starts the provider name of a payment made through an adapter.
const AddonPrefix = "addon:"

// AddonID is the adapter of a provider name, "" for a built-in provider.
func AddonID(provider string) string {
	id, ok := strings.CutPrefix(provider, AddonPrefix)
	if !ok || !validAddonID.MatchString(id) {
		return ""
	}
	return id
}

// Stars is Telegram's own currency; every other provider is a marketplace adapter,
// "addon:<id>".
const Stars = "stars"

// Config is what the admin sets; the adapters' settings are their own (addon.go).
type Config struct {
	// Enabled is the switch for selling at all: off, the bot and the Mini App offer
	// nothing and take no new invoices, while invoices already opened are still applied.
	Enabled bool `json:"enabled"`
	Stars   bool `json:"stars"`
	// AllowNew lets people without a subscription buy one; off: only renewals.
	AllowNew bool `json:"allow_new"`
	// RenewResetsTraffic: a paid renewal also starts a new traffic period; off, the
	// counter keeps running and only the term is extended.
	RenewResetsTraffic bool `json:"renew_resets_traffic"`
	// TrialTariffID is the tariff of the free trial a Telegram account may take once in
	// the bot; 0: no trial. It works with selling off too: it takes no payment.
	TrialTariffID int64 `json:"trial_tariff_id,omitempty"`
	// TrialStars and TrialRub (kopecks) put a price on the trial; both 0: it is free. A
	// priced trial is bought in the bot and the Mini App, once per account like the free
	// one, through the providers that take its price: it needs selling on.
	TrialStars int64 `json:"trial_stars,omitempty"`
	TrialRub   int64 `json:"trial_rub,omitempty"`
	// FreeTariffID is the tariff of the free plan (free.go): unlike the trial, an account
	// takes it again once its previous free subscription has ended; 0: no free plan. It
	// takes no payment, so it works with selling off too.
	FreeTariffID int64 `json:"free_tariff_id,omitempty"`
	// FreeCondition is what the free plan asks of an account: FreeNoCondition, or
	// FreeChannel, a subscription to the bot's channel.
	FreeCondition string `json:"free_condition,omitempty"`
}

// TrialPaid: the trial has a price and is bought, not taken.
func (c Config) TrialPaid() bool { return c.TrialStars > 0 || c.TrialRub > 0 }

var ErrNotForSale = errors.New("not_for_sale")

var ErrNotYours = errors.New("not_yours")

var ErrTooManySubs = errors.New("too_many_subs")

var ErrTooMany = errors.New("too_many_invoices")

// Available says which providers can take a payment right now: selling on, Stars with
// the bot running, and the adapters that are on, set up and running.
type Available struct {
	Stars bool
	// Addons are the adapters that take rubles now, by id.
	Addons []string
}

func (a Available) Any() bool { return a.Stars || a.Rub() }

// Rub: some provider takes rubles.
func (a Available) Rub() bool { return len(a.Addons) > 0 }

func (a Available) has(provider string) bool {
	if provider == Stars {
		return a.Stars
	}
	id := AddonID(provider)
	return id != "" && slices.Contains(a.Addons, id)
}

// Offer is a tariff on sale with the terms a buyer can pay for now. Stars and Rub are
// those of the first of Terms.
type Offer struct {
	Tariff db.Tariff
	Terms  []OfferTerm // at least one
	First  int64       // days of the tariff's first term, which Terms may not hold
	Stars  int64       // 0: not for Stars
	Rub    int64       // kopecks; 0: not for rubles
}

// OfferTerm is a term of a tariff on sale with the prices the available providers take.
type OfferTerm struct {
	Days  int64
	Stars int64 // 0: not for Stars
	Rub   int64 // kopecks; 0: not for rubles
}

// Term is the offer's term of so many days; nil days: the tariff's first term, the one an
// invoice without days is for (not there when no available provider takes its price).
func (o Offer) Term(days *int64) (OfferTerm, bool) {
	first := o.First
	if days == nil {
		days = &first
	}
	for _, t := range o.Terms {
		if t.Days == *days {
			return t, true
		}
	}
	return OfferTerm{}, false
}

// InvoiceRequest: who buys which tariff for which term with what. UserID 0 buys a new
// subscription.
type InvoiceRequest struct {
	TgID      int64
	UserID    int64
	TariffID  int64
	TermDays  *int64 // the term's days; nil: the tariff's first term
	Provider  string
	PromoCode string
}

// prices are the prices of an item the available providers take: 0 where none does.
func (av Available) prices(stars, rub sql.NullInt64) (inStars, inRub int64) {
	if av.Stars && stars.Valid {
		inStars = stars.Int64
	}
	if av.Rub() && rub.Valid {
		inRub = rub.Int64
	}
	return inStars, inRub
}

// price is what a buyer pays with provider for an item with these prices: Stars for
// Stars, rubles for the others; ok false when the provider cannot take it now.
func (av Available) price(provider string, stars, rub sql.NullInt64) (amount int64, currency string, ok bool) {
	switch {
	case provider == Stars && av.Stars && stars.Valid:
		return stars.Int64, "XTR", true
	case provider != Stars && av.has(provider) && rub.Valid:
		return rub.Int64, "RUB", true
	}
	return 0, "", false
}

// Describe is tariff t bought for a term of days in a line, "30 days · 100 GB · 3
// devices", in lang ("en", else Russian): invoices, the bot and the Mini App show it.
func Describe(t db.Tariff, days int64, lang string) string {
	return TermLabel(t, days, lang) + " · " + DescribeLimits(t, lang)
}

// TermLabel is a term of tariff t in a few words: "30 days", "3 months" for a tariff that
// ends on a billing day, "no end date".
func TermLabel(t db.Tariff, days int64, lang string) string {
	pick := func(ru, en string) string {
		if lang == "en" {
			return en
		}
		return ru
	}
	switch {
	case days <= 0:
		return pick("бессрочно", "no end date")
	case t.BillingDay.Valid:
		n := domain.TermMonths(days)
		if n == 1 {
			return pick("1 мес.", "1 month")
		}
		return fmt.Sprintf(pick("%d мес.", "%d months"), n)
	}
	return fmt.Sprintf(pick("%d дн.", "%d days"), days)
}

// DescribeOffer is a tariff on sale in a line: Describe with its one term, or the range
// of its terms, "7 days – 90 days · 100 GB · 3 devices".
func DescribeOffer(o Offer, lang string) string {
	switch len(o.Terms) {
	case 0:
		return Describe(o.Tariff, o.Tariff.DurationDays, lang)
	case 1:
		return Describe(o.Tariff, o.Terms[0].Days, lang)
	}
	var lo, hi int64 = -1, -1
	forever := false
	for _, t := range o.Terms {
		if t.Days <= 0 {
			forever = true
			continue
		}
		if lo < 0 || t.Days < lo {
			lo = t.Days
		}
		hi = max(hi, t.Days)
	}
	if forever {
		hi = 0
	}
	return TermLabel(o.Tariff, lo, lang) + " – " + TermLabel(o.Tariff, hi, lang) + " · " + DescribeLimits(o.Tariff, lang)
}

// DescribeLimits is what tariff t gives whatever the term: "100 GB · 3 devices".
func DescribeLimits(t db.Tariff, lang string) string {
	en := lang == "en"
	pick := func(ru, en_ string) string {
		if en {
			return en_
		}
		return ru
	}
	parts := []string{}
	if t.TrafficLimit.Valid {
		parts = append(parts, fmt.Sprintf(pick("%d ГБ", "%d GB"), t.TrafficLimit.Int64>>30))
	} else {
		parts = append(parts, pick("трафик без лимита", "unlimited traffic"))
	}
	if t.DeviceLimit.Valid && t.DeviceMode == domain.DeviceModeOnline {
		// The buyer is not told of places: any number of devices, so many online at once.
		parts = append(parts, fmt.Sprintf(pick("устройств одновременно: %d", "devices at once: %d"), t.DeviceLimit.Int64))
	} else if t.DeviceLimit.Valid {
		parts = append(parts, fmt.Sprintf(pick("устройств: %d", "devices: %d"), t.DeviceLimit.Int64))
	}
	if t.SpeedLimit.Valid {
		parts = append(parts, fmt.Sprintf(pick("до %d Мбит/с", "up to %d Mbit/s"), t.SpeedLimit.Int64))
	}
	return strings.Join(parts, " · ")
}

// What the free plan asks of an account (Config.FreeCondition).
const (
	FreeNoCondition = ""
	FreeChannel     = "channel" // a subscription to the bot's channel
)

// ErrFreeOff: the admin offers no free plan, its tariff is gone, or it asks for the
// bot's channel while none is set up.
var ErrFreeOff = errors.New("free_off")

// ErrFreeActive: the account's previous free subscription has not ended yet.
var ErrFreeActive = errors.New("free_active")

// ErrFreeChannel: the free plan is for the bot's channel's members, and the account is
// not one.
var ErrFreeChannel = errors.New("free_channel")

// FreeOffer is the free plan as an account sees it: its tariff, and the link to the
// channel it asks the account to join ("" when it asks nothing).
type FreeOffer struct {
	Tariff  db.Tariff
	Channel string
}

// KindPackage is a payment for a traffic package.
const KindPackage = "package"

// PackageOffer is a package on sale for one subscription, with the prices the available
// providers take.
type PackageOffer struct {
	Package db.TrafficPackage
	Pool    string // the pool's name; "" for the main traffic
	Stars   int64  // 0: not for Stars
	Rub     int64  // kopecks; 0: not for rubles
}

// PackageRequest: the owner of subscription UserID buys a package with Provider.
type PackageRequest struct {
	TgID      int64
	UserID    int64
	PackageID int64
	Provider  string
	PromoCode string
}

// DescribePackage is a package in a line, "50 GB · WL · until the period ends", in lang
// ("en", else Russian); pool is the pool's name, "" for the main traffic.
func DescribePackage(p db.TrafficPackage, pool, lang string) string {
	en := lang == "en"
	pick := func(ru, en_ string) string {
		if en {
			return en_
		}
		return ru
	}
	size := fmt.Sprintf(pick("%s ГБ", "%s GB"), gigabytes(p.Bytes, en))
	target := pool
	if target == "" {
		target = pick("основной трафик", "main traffic")
	}
	var term string
	switch p.Lifetime {
	case domain.LifetimePeriod:
		term = pick("до конца периода", "until the period ends")
	case domain.LifetimeDays:
		term = fmt.Sprintf(pick("на %d дн.", "for %d days"), p.Days)
	default:
		term = pick("пока не израсходован", "until used up")
	}
	return strings.Join([]string{size, target, term}, " · ")
}

// gigabytes: "50", or "1.5" ("1,5" in Russian) for a part of a GB.
func gigabytes(n int64, en bool) string {
	if n%domain.GiB == 0 {
		return fmt.Sprint(n / domain.GiB)
	}
	s := strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/float64(domain.GiB)), ".0")
	if !en {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// ErrTrialOff: the admin offers no trial, or its tariff is gone.
var ErrTrialOff = errors.New("trial_off")

// ErrTrialUsed: this Telegram account took its trial already, or is a customer (it
// has a subscription or paid for one): a trial is for people who have not tried yet.
var ErrTrialUsed = errors.New("trial_used")

// TrialOffer is the priced trial an account may buy: its tariff and the prices the
// available providers take (0: not for that currency).
type TrialOffer struct {
	Tariff db.Tariff
	Stars  int64
	Rub    int64 // kopecks
}

// validAddonID is mikan's addons.ValidID: adapter ids are path and provider-name safe.
var validAddonID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
