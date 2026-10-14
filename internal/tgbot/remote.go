package tgbot

import (
	"context"
	"time"

	"subsbot/internal/billing"
	"subsbot/internal/db"
	"subsbot/internal/domain"
)

// The methods of Remote, one call each (rpc.go). A method whose interface has no error to
// give (Config, Brand, ...) gives its zero value when the panel cannot be reached: the
// screens show the defaults rather than nothing.

func (r *Remote) Config(ctx context.Context) Config {
	var c Config
	if err := r.call(ctx, "Config", nil, &c); err != nil {
		return Default("")
	}
	return c
}

func (r *Remote) Brand(ctx context.Context) string {
	var s string
	_ = r.call(ctx, "Brand", nil, &s)
	return s
}

func (r *Remote) SupportURL(ctx context.Context) string {
	var s string
	_ = r.call(ctx, "SupportURL", nil, &s)
	return s
}

func (r *Remote) SubURL(ctx context.Context, u db.User) string {
	var s string
	_ = r.call(ctx, "SubURL", []any{u}, &s)
	return s
}

func (r *Remote) MiniAppURL(ctx context.Context) string {
	var s string
	_ = r.call(ctx, "MiniAppURL", nil, &s)
	return s
}

func (r *Remote) Member(ctx context.Context, chat string, tgID int64, fresh bool) (bool, error) {
	var in bool
	err := r.call(ctx, "Member", []any{chat, tgID, fresh}, &in)
	return in, err
}

func (r *Remote) Context(ctx context.Context, tgID int64) (Account, error) {
	var a Account
	err := r.call(ctx, "Context", []any{tgID}, &a)
	return a, err
}

func (r *Remote) Chat(ctx context.Context, tgID int64) (db.TgChat, error) {
	var c db.TgChat
	err := r.call(ctx, "Chat", []any{tgID}, &c)
	return c, err
}

func (r *Remote) SetMenu(ctx context.Context, tgID, msgID int64) error {
	return r.call(ctx, "SetMenu", []any{tgID, msgID})
}

func (r *Remote) SetCurrent(ctx context.Context, tgID, userID int64) error {
	return r.call(ctx, "SetCurrent", []any{tgID, userID})
}

func (r *Remote) UserOfCode(ctx context.Context, code string) (int64, error) {
	var id int64
	err := r.call(ctx, "UserOfCode", []any{code}, &id)
	return id, err
}

func (r *Remote) UserOfAddress(ctx context.Context, urlPath string) (int64, bool) {
	var (
		id int64
		ok bool
	)
	if err := r.call(ctx, "UserOfAddress", []any{urlPath}, &id, &ok); err != nil {
		return 0, false
	}
	return id, ok
}

func (r *Remote) Link(ctx context.Context, tgID, userID int64) (Linked, error) {
	var l Linked
	err := r.call(ctx, "Link", []any{tgID, userID}, &l)
	return l, err
}

func (r *Remote) Owner(ctx context.Context, userID int64) (int64, error) {
	var tg int64
	err := r.call(ctx, "Owner", []any{userID}, &tg)
	return tg, err
}

func (r *Remote) Transfer(ctx context.Context, userID, to int64) error {
	return r.call(ctx, "Transfer", []any{userID, to})
}

func (r *Remote) Unlink(ctx context.Context, userID int64) error {
	return r.call(ctx, "Unlink", []any{userID})
}

func (r *Remote) User(ctx context.Context, id int64) (db.User, error) {
	var u db.User
	err := r.call(ctx, "User", []any{id}, &u)
	return u, err
}

func (r *Remote) GrantsLeft(ctx context.Context, userID int64, now time.Time) (domain.GrantsLeft, error) {
	var g domain.GrantsLeft
	err := r.call(ctx, "GrantsLeft", []any{userID, now}, &g)
	return g, err
}

func (r *Remote) Pools(ctx context.Context, userID int64, now time.Time) ([]Pool, error) {
	var p []Pool
	err := r.call(ctx, "Pools", []any{userID, now}, &p)
	return p, err
}

func (r *Remote) DeviceBinding(ctx context.Context) bool {
	var on bool
	_ = r.call(ctx, "DeviceBinding", nil, &on)
	return on
}

func (r *Remote) DeviceCount(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := r.call(ctx, "DeviceCount", []any{userID}, &n)
	return n, err
}

func (r *Remote) Devices(ctx context.Context, userID int64) ([]db.BoundDevice, error) {
	var d []db.BoundDevice
	err := r.call(ctx, "Devices", []any{userID}, &d)
	return d, err
}

func (r *Remote) DeviceTraffic(ctx context.Context, userID int64) (map[int64]int64, error) {
	var m map[int64]int64
	err := r.call(ctx, "DeviceTraffic", []any{userID}, &m)
	return m, err
}

func (r *Remote) UnbindState(ctx context.Context, userID int64) (domain.UnbindState, error) {
	var s domain.UnbindState
	err := r.call(ctx, "UnbindState", []any{userID}, &s)
	return s, err
}

func (r *Remote) Unbind(ctx context.Context, userID, deviceID int64) error {
	return r.call(ctx, "Unbind", []any{userID, deviceID})
}

func (r *Remote) ReissueState(ctx context.Context, userID int64) (domain.ReissueState, error) {
	var s domain.ReissueState
	err := r.call(ctx, "ReissueState", []any{userID}, &s)
	return s, err
}

func (r *Remote) Reissue(ctx context.Context, userID int64) error {
	return r.call(ctx, "Reissue", []any{userID})
}

func (r *Remote) Shop() Shop { return r.shop }

func (r *Remote) Tariff(ctx context.Context, id int64) (db.Tariff, error) {
	var t db.Tariff
	err := r.call(ctx, "Tariff", []any{id}, &t)
	return t, err
}

// remoteShop is Shop over the Panel API ("Shop.<method>").
type remoteShop struct{ r *Remote }

func (s remoteShop) Config(ctx context.Context) billing.Config {
	var c billing.Config
	_ = s.r.call(ctx, "Shop.Config", nil, &c)
	return c
}

func (s remoteShop) Offers(ctx context.Context) ([]billing.Offer, billing.Available, error) {
	var (
		o []billing.Offer
		a billing.Available
	)
	err := s.r.call(ctx, "Shop.Offers", nil, &o, &a)
	return o, a, err
}

func (s remoteShop) AddonName(ctx context.Context, id, lang string) string {
	var n string
	_ = s.r.call(ctx, "Shop.AddonName", []any{id, lang}, &n)
	return n
}

func (s remoteShop) Invoice(ctx context.Context, req billing.InvoiceRequest) (db.Payment, error) {
	var p db.Payment
	err := s.r.call(ctx, "Shop.Invoice", []any{req}, &p)
	return p, err
}

func (s remoteShop) PackageOffers(ctx context.Context, userID int64) ([]billing.PackageOffer, billing.Available, error) {
	var (
		o []billing.PackageOffer
		a billing.Available
	)
	err := s.r.call(ctx, "Shop.PackageOffers", []any{userID}, &o, &a)
	return o, a, err
}

func (s remoteShop) PackageInvoice(ctx context.Context, req billing.PackageRequest) (db.Payment, error) {
	var p db.Payment
	err := s.r.call(ctx, "Shop.PackageInvoice", []any{req}, &p)
	return p, err
}

func (s remoteShop) TrialTariff(ctx context.Context) (db.Tariff, bool) {
	var (
		t  db.Tariff
		ok bool
	)
	if err := s.r.call(ctx, "Shop.TrialTariff", nil, &t, &ok); err != nil {
		return db.Tariff{}, false
	}
	return t, ok
}

func (s remoteShop) TrialOpen(ctx context.Context, tgID int64) bool {
	var ok bool
	_ = s.r.call(ctx, "Shop.TrialOpen", []any{tgID}, &ok)
	return ok
}

func (s remoteShop) TrialAllowed(ctx context.Context, tgID int64) bool {
	var ok bool
	_ = s.r.call(ctx, "Shop.TrialAllowed", []any{tgID}, &ok)
	return ok
}

func (s remoteShop) Trial(ctx context.Context, tgID int64) (db.User, error) {
	var u db.User
	err := s.r.call(ctx, "Shop.Trial", []any{tgID}, &u)
	return u, err
}

func (s remoteShop) TrialOffer(ctx context.Context, tgID int64) (billing.TrialOffer, billing.Available, bool) {
	var (
		o  billing.TrialOffer
		a  billing.Available
		ok bool
	)
	if err := s.r.call(ctx, "Shop.TrialOffer", []any{tgID}, &o, &a, &ok); err != nil {
		return billing.TrialOffer{}, billing.Available{}, false
	}
	return o, a, ok
}

func (s remoteShop) TrialInvoice(ctx context.Context, tgID int64, provider string) (db.Payment, error) {
	var p db.Payment
	err := s.r.call(ctx, "Shop.TrialInvoice", []any{tgID, provider}, &p)
	return p, err
}

func (s remoteShop) FreeOffer(ctx context.Context, tgID int64) (billing.FreeOffer, bool) {
	var (
		o  billing.FreeOffer
		ok bool
	)
	if err := s.r.call(ctx, "Shop.FreeOffer", []any{tgID}, &o, &ok); err != nil {
		return billing.FreeOffer{}, false
	}
	return o, ok
}

func (s remoteShop) FreePlan(ctx context.Context, tgID int64) (db.User, error) {
	var u db.User
	err := s.r.call(ctx, "Shop.FreePlan", []any{tgID}, &u)
	return u, err
}
