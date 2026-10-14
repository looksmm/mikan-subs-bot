// Code copied from mikan (domain) by tools/extract: the declarations the bot needs.

package domain

import (
	"database/sql"
	"errors"
	"subsbot/internal/db"
	"time"
)

// dayOfMonth is day d of the month at clock's time of day, or the month's last day.
func dayOfMonth(year int, month time.Month, d int, clock time.Time) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(year, month, min(d, last), clock.Hour(), clock.Minute(), clock.Second(), 0, time.UTC)
}

// TermMonths turns a tariff's duration into months for a billing day: 30 days is one.
func TermMonths(days int64) int {
	return max(1, int((days+15)/30))
}

// MonthPeriodStart is when the current monthly traffic period began: the last billing
// day (or the 1st without one) at 00:00 UTC not after now.
func MonthPeriodStart(now time.Time, billingDay sql.NullInt64) time.Time {
	d := 1
	if billingDay.Valid {
		d = int(billingDay.Int64)
	}
	now = now.UTC()
	start := dayOfMonth(now.Year(), now.Month(), d, time.Time{})
	if start.After(now) {
		start = dayOfMonth(now.Year(), now.Month()-1, d, time.Time{})
	}
	return start
}

// nextMonthPeriod is when the monthly traffic period that began at start ends.
func nextMonthPeriod(start time.Time, billingDay sql.NullInt64) time.Time {
	d := 1
	if billingDay.Valid {
		d = int(billingDay.Int64)
	}
	return dayOfMonth(start.Year(), start.Month()+1, d, time.Time{})
}

// How a device limit counts, per tariff and per user (users.device_mode).
const (
	// DeviceModeBound: the devices bound to the subscription by their ids are the places
	// of the limit; a new one is refused when they are all taken.
	DeviceModeBound = "bound"
	// DeviceModeOnline: any number of devices may bind, and only the ones online at once
	// count; the nodes enforce it (Policy.DeviceLimit).
	DeviceModeOnline = "online"
)

// BindsDevices says whether the device limit of a user with this mode is the number of
// bound devices; in the online mode it is not.
func BindsDevices(mode string) bool { return mode != DeviceModeOnline }

var ErrUnbindCooldown = errors.New("unbind_cooldown") // the subscriber used up the unbinds the admin's rules allow for now

// Lifetimes of packages and grants.
const (
	LifetimeUsed   = "used"   // until used up
	LifetimePeriod = "period" // until the end of the traffic period it was given in
	LifetimeDays   = "days"   // N days from when it was given
)

// Limits of a package or a grant.
const (
	GiB            = int64(1) << 30
	MinGrantBytes  = GiB
	MaxGrantBytes  = 100 << 40
	MaxGrantDays   = 3650
	maxGrantNoteLn = 200
)

// FieldError is input the domain refuses: Field is the input's name, Code says why.
type FieldError struct{ Field, Code string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Code }

func fieldErr(field, code string) error { return &FieldError{Field: field, Code: code} }

// TrafficLeft is what the user may still use of a quota: the base limit's rest plus the
// active grants. -1: unlimited (no limit), which never touches grants.
func TrafficLeft(limit sql.NullInt64, used, grants int64) int64 {
	if !limit.Valid {
		return -1
	}
	return max(0, limit.Int64-used) + max(0, grants)
}

// GrantsLeft is what is left of the active grants, by user and pool (0: main traffic).
type GrantsLeft map[[2]int64]int64

func (g GrantsLeft) Main(userID int64) int64 { return g[[2]int64{userID, 0}] }

func (g GrantsLeft) Pool(userID, poolID int64) int64 { return g[[2]int64{userID, poolID}] }

// ReissueRules is how the subscriber may replace their own subscription link: so many
// times a period. A link that leaked is the reason to allow it at all; without a limit
// whoever holds the Mini App could keep the subscription unusable for its owner's other
// devices over and over, and every reissue costs a fresh slot and a push to the nodes.
// The admin reissues outside these rules.
type ReissueRules struct {
	// Off: the subscriber reissues nothing themselves, only the admin does.
	Off bool `json:"off,omitempty" doc:"Подписчик не меняет ссылку сам, только админ"`
	// Limit is how many times the subscriber may reissue in Days; 0: as often as they like.
	Limit int `json:"limit" minimum:"0" maximum:"100" doc:"Сколько раз подписчик может сменить ссылку за days; 0: без ограничения"`
	// Days is the window the limit counts in, sliding: a reissue leaves it Days later.
	Days int `json:"days" minimum:"1" maximum:"365" doc:"За сколько дней считается лимит (скользящее окно)"`
}

const (
	MaxReissueLimit = 100
	MaxReissueDays  = 365
)

// Validate checks rules the admin sends.
func (r ReissueRules) Validate() error {
	switch {
	case r.Limit < 0 || r.Limit > MaxReissueLimit:
		return fieldErr("limit", "bad_reissue_limit")
	case r.Days < 1 || r.Days > MaxReissueDays:
		return fieldErr("days", "bad_reissue_days")
	}
	return nil
}

func (r ReissueRules) window() time.Duration { return time.Duration(r.Days) * 24 * time.Hour }

// ReissueState is what the subscriber may do now under the rules.
type ReissueState struct {
	Rules ReissueRules
	// Used is how many reissues of theirs the window holds.
	Used int
	// Next is when they may reissue again; zero: now.
	Next time.Time
}

// Left is how many reissues the window still has room for; -1 without a limit, 0 when the
// subscriber may not reissue at all.
func (s ReissueState) Left() int {
	switch {
	case s.Rules.Off:
		return 0
	case s.Rules.Limit == 0:
		return -1
	}
	return max(0, s.Rules.Limit-s.Used)
}

// ErrReissueOff: the admin reissues links, the subscriber does not.
var ErrReissueOff = errors.New("reissue_off")

// ErrReissueCooldown: the subscriber used up the reissues the admin's rules allow for now.
var ErrReissueCooldown = errors.New("reissue_cooldown")

// UnbindRules is how the subscriber may free places: so many unbinds a period, and how
// long a device they unbound may not take a place again. Without a limit a reseller would
// let buyers in one after another; without the pause an app still open on the unbound
// device fetches the subscription and binds again, and the unbind seems to do nothing.
// The admin unbinds outside these rules.
type UnbindRules struct {
	// Off: the subscriber unbinds nothing themselves, only the admin does.
	Off bool `json:"off,omitempty" doc:"Подписчик не отвязывает устройства сам, только админ"`
	// Limit is how many devices the subscriber may unbind in Days; 0: as many as they like.
	Limit int `json:"limit" minimum:"0" maximum:"100" doc:"Сколько устройств подписчик может отвязать за days; 0 — без ограничения"`
	// Days is the window the limit counts in, sliding: an unbind leaves it Days later.
	Days int `json:"days" minimum:"1" maximum:"365" doc:"За сколько дней считается лимит (скользящее окно)"`
	// ReturnHours is how long a device the subscriber unbound may not bind again; 0: at
	// once, ReturnNever: until the admin lets it. The admin sees it among the bans and may
	// let it in earlier.
	ReturnHours int `json:"return_hours" minimum:"-1" maximum:"720" doc:"Сколько часов отвязанное подписчиком устройство не может привязаться снова; 0 — сразу, -1 — пока админ не разрешит"`
}

// ReturnNever: a device the subscriber unbound comes back only when the admin lets it.
const ReturnNever = -1

const (
	MaxUnbindLimit = 100
	MaxUnbindDays  = 365
	MaxReturnHours = 30 * 24
)

// Validate checks rules the admin sends.
func (r UnbindRules) Validate() error {
	switch {
	case r.Limit < 0 || r.Limit > MaxUnbindLimit:
		return fieldErr("limit", "bad_unbind_limit")
	case r.Days < 1 || r.Days > MaxUnbindDays:
		return fieldErr("days", "bad_unbind_days")
	case r.ReturnHours < ReturnNever || r.ReturnHours > MaxReturnHours:
		return fieldErr("return_hours", "bad_return_hours")
	}
	return nil
}

func (r UnbindRules) window() time.Duration { return time.Duration(r.Days) * 24 * time.Hour }

// UnbindState is what the subscriber may do now under the rules.
type UnbindState struct {
	Rules UnbindRules
	// Used is how many unbinds of theirs the window holds.
	Used int
	// Next is when they may unbind again; zero: now.
	Next time.Time
}

// Left is how many unbinds the window still has room for; -1 without a limit, 0 when the
// subscriber may not unbind at all.
func (s UnbindState) Left() int {
	switch {
	case s.Rules.Off:
		return 0
	case s.Rules.Limit == 0:
		return -1
	}
	return max(0, s.Rules.Limit-s.Used)
}

// ErrUnbindOff: the admin unbinds the subscriber's devices, the subscriber does not.
var ErrUnbindOff = errors.New("unbind_off")

const (
	StateActive   = "active"
	StateExpiring = "expiring"
	StateLimited  = "limited"
	StateExpired  = "expired"
	StateDisabled = "disabled"

	expiringWindow = 7 * 24 * time.Hour
	day            = int64(24 * time.Hour / time.Second)
)

var ErrNotFound = errors.New("not found")

// State derives what the user can do right now; limited and expired are never stored.
// grants is what is left of the user's active main grants (GrantsLeft.Main): a user past
// the base quota with grants left is not limited.
func State(u db.User, grants int64, now time.Time) string {
	switch {
	case u.Status == "disabled":
		return StateDisabled
	case u.ExpiresAt.Valid && now.Unix() >= u.ExpiresAt.Int64:
		return StateExpired
	case TrafficLeft(u.TrafficLimit, u.UsedUp+u.UsedDown, grants) == 0:
		return StateLimited
	case u.ExpiresAt.Valid && time.Unix(u.ExpiresAt.Int64, 0).Sub(now) <= expiringWindow:
		return StateExpiring
	}
	return StateActive
}

// NextReset is when the traffic counter of the current period drops to zero.
func NextReset(u db.User, now time.Time) (time.Time, bool) {
	switch u.ResetStrategy {
	case "month_start":
		// Monthly: on the billing day, or the 1st without one.
		return nextMonthPeriod(MonthPeriodStart(now, u.BillingDay), u.BillingDay), true
	case "period":
		return time.Unix(u.PeriodStart+max(u.PeriodDays, 1)*day, 0).UTC(), true
	}
	return time.Time{}, false
}
