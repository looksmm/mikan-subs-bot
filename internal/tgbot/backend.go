package tgbot

import (
	"context"
	"errors"
	"time"

	"subsbot/internal/billing"
	"subsbot/internal/db"
	"subsbot/internal/domain"
)

// The bot's screens, which run as the bot addon, see the panel through Backend: its data
// (who an account is, its subscriptions, what it may buy) and the channel check. Remote is
// that over the Panel API (docs/pipeline/addons.md, section 13), Local the panel's side of
// the same calls (RPCHandler), so the methods follow those endpoints. What the screens send
// goes through the Bot API proxy.

// Backend is the panel as the bot's screens use it.
type Backend interface {
	// Config is the admin's setup of the bot, or the default one in the panel's language
	// (GET /settings).
	Config(ctx context.Context) Config
	// Brand and SupportURL are the panel's settings as saved: "" when not set.
	Brand(ctx context.Context) string
	SupportURL(ctx context.Context) string
	// SubURL is u's subscription link; "" while the panel has no public address.
	SubURL(ctx context.Context, u db.User) string
	// MiniAppURL is where the Mini App opens; "" when Telegram could not load it (GET
	// /tg/mini-app).
	MiniAppURL(ctx context.Context) string
	// Member says whether tgID is in the bot's channel chat (GET /tg/subscribed). The
	// screens may ask about the bot's own channel only.
	Member(ctx context.Context, chat string, tgID int64, fresh bool) (bool, error)

	// Context is a Telegram account: its chat and the subscriptions linked to it (GET
	// /tg/context).
	Context(ctx context.Context, tgID int64) (Account, error)
	// Chat is the account's chat alone: its menu message, its current subscription and
	// whether it blocked the bot.
	Chat(ctx context.Context, tgID int64) (db.TgChat, error)
	// SetMenu and SetCurrent keep the chat's menu message and the subscription it shows.
	SetMenu(ctx context.Context, tgID, msgID int64) error
	SetCurrent(ctx context.Context, tgID, userID int64) error
	// UserOfCode is the subscription a /start code names (tgate.ErrLinkExpired, else
	// tgate.ErrLinkInvalid); UserOfAddress the one an address path leads to, the panel's own
	// or an old panel's (POST /tg/link/redeem).
	UserOfCode(ctx context.Context, code string) (int64, error)
	UserOfAddress(ctx context.Context, urlPath string) (int64, bool)
	// Link ties subscription userID to account tgID, unless another account it can ask
	// owns it (LinkTaken) or tgID holds tgate.MaxLinks already.
	Link(ctx context.Context, tgID, userID int64) (Linked, error)
	// Owner is the account a subscription is linked to.
	Owner(ctx context.Context, userID int64) (int64, error)
	// Transfer moves subscription userID to account to and shows it there, its owner having
	// said yes: ErrLinkLimit when that account holds tgate.MaxLinks already (POST
	// /tg/transfer).
	Transfer(ctx context.Context, userID, to int64) error
	// Unlink takes a subscription off its account; the subscription stays (POST /tg/unlink).
	Unlink(ctx context.Context, userID int64) error

	// User is one subscription (GET /users/{id}).
	User(ctx context.Context, id int64) (db.User, error)
	// GrantsLeft is what is left of the traffic packages of a subscription.
	GrantsLeft(ctx context.Context, userID int64, now time.Time) (domain.GrantsLeft, error)
	// Pools are the subscription's traffic pools with a limit.
	Pools(ctx context.Context, userID int64, now time.Time) ([]Pool, error)
	// DeviceBinding: subscribers' devices are bound (the panel's switch).
	DeviceBinding(ctx context.Context) bool
	// DeviceCount, Devices and DeviceTraffic are a subscription's bound devices (GET
	// /users/{id}/devices); DeviceTraffic is by device.
	DeviceCount(ctx context.Context, userID int64) (int64, error)
	Devices(ctx context.Context, userID int64) ([]db.BoundDevice, error)
	DeviceTraffic(ctx context.Context, userID int64) (map[int64]int64, error)
	// UnbindState and Unbind: the subscriber's own unbinding, under the panel's rules
	// (POST /users/{id}/devices/{dev}/unbind).
	UnbindState(ctx context.Context, userID int64) (domain.UnbindState, error)
	Unbind(ctx context.Context, userID, deviceID int64) error
	// ReissueState and Reissue: the subscriber's own new link, under the panel's rules
	// (POST /users/{id}/reissue).
	ReissueState(ctx context.Context, userID int64) (domain.ReissueState, error)
	Reissue(ctx context.Context, userID int64) error

	// Shop is what the panel sells in the bot (GET /offers, POST /billing/*).
	Shop() Shop
	// Tariff is one tariff, for what a trial, a free plan or an invoice gives.
	Tariff(ctx context.Context, id int64) (db.Tariff, error)
}

// PanelBackend is Backend and what only the panel's side of the bot reads: the notices
// about the term and the traffic (which were told, tg_notices) and the broadcasts' targets.
// The addon never asks for these, so Remote has none of them and the Panel API opens none:
// a list of every subscriber's account is not the addon's to read.
type PanelBackend interface {
	Backend
	// Notices are the subscriptions that may get a notice now.
	Notices(ctx context.Context, now time.Time) ([]db.ListNoticeSubscriptionsRow, error)
	NoticeSent(ctx context.Context, userID int64, kind string, period int64) (bool, error)
	NoticeDelivered(ctx context.Context, userID int64, kind string, period int64, at time.Time) error
	PruneNotices(ctx context.Context, before time.Time) error
	// BroadcastTargets are the accounts a broadcast goes to.
	BroadcastTargets(ctx context.Context) ([]int64, error)
}

// Shop is the panel's billing as the bot's screens use it (*billing.Service).
type Shop interface {
	Config(ctx context.Context) billing.Config
	Offers(ctx context.Context) ([]billing.Offer, billing.Available, error)
	AddonName(ctx context.Context, id, lang string) string
	Invoice(ctx context.Context, req billing.InvoiceRequest) (db.Payment, error)
	PackageOffers(ctx context.Context, userID int64) ([]billing.PackageOffer, billing.Available, error)
	PackageInvoice(ctx context.Context, req billing.PackageRequest) (db.Payment, error)
	TrialTariff(ctx context.Context) (db.Tariff, bool)
	TrialOpen(ctx context.Context, tgID int64) bool
	TrialAllowed(ctx context.Context, tgID int64) bool
	Trial(ctx context.Context, tgID int64) (db.User, error)
	TrialOffer(ctx context.Context, tgID int64) (billing.TrialOffer, billing.Available, bool)
	TrialInvoice(ctx context.Context, tgID int64, provider string) (db.Payment, error)
	FreeOffer(ctx context.Context, tgID int64) (billing.FreeOffer, bool)
	FreePlan(ctx context.Context, tgID int64) (db.User, error)
}

// Account is a Telegram account as the bot sees it.
type Account struct {
	Chat    db.TgChat // its chat; HasChat false before it wrote to the bot
	HasChat bool
	Links   []db.User // its subscriptions
}

// LinkStatus is how Backend.Link went.
type LinkStatus int

const (
	LinkDone    LinkStatus = iota // linked to the account and shown there
	LinkAlready                   // the account's already: shown there now
	LinkLimit                     // the account holds tgate.MaxLinks already
	LinkTaken                     // another account owns it and can be asked: Linked.Owner
)

// Linked is the subscription Backend.Link was about and what came of it.
type Linked struct {
	Status LinkStatus
	User   db.User
	Owner  int64
}

// Pool is a traffic pool of a subscription, with its limit.
type Pool struct {
	Name      string
	Used      int64
	Limit     int64
	Extra     int64 // left of the pool's packages
	Exhausted bool
}

var (
	// ErrLinkLimit: the account holds tgate.MaxLinks subscriptions already.
	ErrLinkLimit = errors.New("max_links")
	// ErrNotChannel: Backend.Member was asked about a chat that is not the bot's channel.
	ErrNotChannel = errors.New("not_the_channel")
	// ErrNotTalking: the Panel API refused a call about an account that is not talking to the
	// bot now (the addon reaches only those; Remote only).
	ErrNotTalking = errors.New("not_talking")
)

// The addon's backend is what the screens use. (The panel's own pieces, Local and the
// billing service, stay in mikan.)
var _ Backend = (*Remote)(nil)
