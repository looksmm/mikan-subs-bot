package tgbot

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"subsbot/internal/db"
	"subsbot/internal/domain"
	"subsbot/internal/events"
	"subsbot/internal/tgate"
)

// PanelDeps is what the panel's side of the bot needs.
type PanelDeps struct {
	Backend PanelBackend
	// Outbox is the running bot's outbox; nil while the bot is off.
	Outbox func() *tgate.Outbox
	// Emit tells the bot addon of a notice it should send (the panel's feed of events,
	// internal/panel/events).
	Emit Emit
	Log  *slog.Logger
	Now  func() time.Time
}

// Panel is the bot as the panel keeps it: the admin's setup, the notices it tells the bot
// addon of, and the broadcasts. The screens, the payment messages and the sending of the
// notices are the addon's (Bot).
type Panel struct {
	d PanelDeps

	mu    sync.Mutex
	bcast BroadcastProgress
}

// BroadcastProgress is the last broadcast as the admin panel shows it.
type BroadcastProgress struct {
	Total, Sent, Failed int
	Started             time.Time
}

// Active: some of it is still being sent.
func (p BroadcastProgress) Active() bool { return p.Sent+p.Failed < p.Total }

func NewPanel(d PanelDeps) *Panel { return &Panel{d: d} }

// Run tells the addon of the notices that are due while ctx lasts.
func (p *Panel) Run(ctx context.Context) {
	notify := time.NewTicker(10 * time.Minute)
	defer notify.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-notify.C:
			p.Notify(ctx)
		}
	}
}

// Config is the admin's setup, or the default one in the panel's default language.
func (p *Panel) Config(ctx context.Context) Config { return p.d.Backend.Config(ctx) }

// MiniAppURL is where the bot's Mini App opens; "" when Telegram could not load it.
func (p *Panel) MiniAppURL(ctx context.Context) string {
	return miniAppURL(ctx, p.d.Backend, p.Config(ctx))
}

// A notice goes once per subscription term or traffic period (tg_notices).
type notice struct {
	kind   string
	period int64
}

// due are the notices a subscription has earned now; grants is what is left of its main
// traffic packages: traffic runs out only when they do too.
func due(u db.User, grants int64, now time.Time, n Notify) []notice {
	if u.Status == "disabled" {
		return nil
	}
	var out []notice
	if u.ExpiresAt.Valid {
		exp := time.Unix(u.ExpiresAt.Int64, 0)
		left := exp.Sub(now)
		switch {
		case left <= 0 && left > -3*24*time.Hour && n.Expired:
			out = append(out, notice{kind: "expired", period: u.ExpiresAt.Int64})
		case left > 0 && left <= 24*time.Hour && n.Expire1d:
			out = append(out, notice{kind: "expire_1d", period: u.ExpiresAt.Int64})
		case left > 24*time.Hour && left <= 3*24*time.Hour && n.Expire3d:
			out = append(out, notice{kind: "expire_3d", period: u.ExpiresAt.Int64})
		}
	}
	if u.TrafficLimit.Valid && u.TrafficLimit.Int64 > 0 {
		used := u.UsedUp + u.UsedDown
		switch {
		case domain.TrafficLeft(u.TrafficLimit, used, grants) == 0 && n.Traffic100:
			out = append(out, notice{kind: "traffic_100", period: u.PeriodStart})
		case grants <= 0 && used*10 >= u.TrafficLimit.Int64*9 && n.Traffic90:
			out = append(out, notice{kind: "traffic_90", period: u.PeriodStart})
		}
	}
	return out
}

// noticesKept is how long a told notice is remembered (tg_notices).
const noticesKept = 90 * 24 * time.Hour

// Notify tells the bot addon, as events, of the notices that are due, by the admin's
// switches and once each (tg_notices); the addon words and sends them. Once a day, in the
// small hours, it forgets the notices told long ago.
func (p *Panel) Notify(ctx context.Context) {
	cfg := p.Config(ctx)
	now := p.d.Now()
	subs, err := p.d.Backend.Notices(ctx, now)
	if err != nil {
		p.d.Log.Error("telegram: notices", "err", err)
		return
	}
	for _, l := range subs {
		u := l.User
		for _, n := range due(u, l.GrantsLeft, now, cfg.Notify) {
			sent, err := p.d.Backend.NoticeSent(ctx, u.ID, n.kind, n.period)
			if err != nil || sent {
				continue
			}
			typ, data := events.UserExpiring, events.Notice{ExpiresAt: u.ExpiresAt.Int64}
			switch n.kind {
			case "expire_3d":
				data.Days = 3
			case "expire_1d":
				data.Days = 1
			case "expired":
				typ = events.UserExpired
			case "traffic_90":
				typ, data = events.Traffic90, events.Notice{PeriodFrom: n.period}
			case "traffic_100":
				typ, data = events.Traffic100, events.Notice{PeriodFrom: n.period}
			}
			dedupe := "notice:" + n.kind + ":" + strconv.FormatInt(u.ID, 10) + ":" + strconv.FormatInt(n.period, 10)
			if err := p.d.Emit(ctx, typ, u.ID, l.TgID, data, dedupe); err != nil {
				p.d.Log.Error("telegram: notice event", "user", u.ID, "err", err)
				continue
			}
			if err := p.d.Backend.NoticeDelivered(ctx, u.ID, n.kind, n.period, now); err != nil {
				p.d.Log.Error("telegram: notices", "err", err)
			}
		}
	}
	// A round comes every ten minutes: this hour's first one prunes.
	if now.Hour() == 3 && now.Minute() < 10 {
		if err := p.d.Backend.PruneNotices(ctx, now.Add(-noticesKept)); err != nil {
			p.d.Log.Error("telegram: prune notices", "err", err)
		}
	}
}

// ErrBusy: a broadcast is being sent; one at a time.
var ErrBusy = errors.New("broadcast_busy")

// Broadcast queues the admin's text for every account with a subscription and returns
// how many will get it. The outbox sends it at 20 messages a second at most, after the
// replies and the notices; the admin panel shows the progress.
func (p *Panel) Broadcast(ctx context.Context, text string) (int, error) {
	out := p.d.Outbox()
	if out == nil {
		return 0, tgate.ErrOff
	}
	p.mu.Lock()
	if p.bcast.Active() {
		p.mu.Unlock()
		return 0, ErrBusy
	}
	targets, err := p.d.Backend.BroadcastTargets(ctx)
	if err != nil {
		p.mu.Unlock()
		return 0, err
	}
	p.bcast = BroadcastProgress{Total: len(targets), Started: p.d.Now()}
	p.mu.Unlock()
	body := render(text, map[string]string{"brand": brandOf(ctx, p.d.Backend)})
	queue := func(chat int64) {
		out.Bulk(chat, func(ctx context.Context, c *tgate.Client) error {
			_, err := c.Send(ctx, chat, body, nil, false)
			return err
		}, func(err error) {
			p.mu.Lock()
			if err == nil {
				p.bcast.Sent++
			} else {
				p.bcast.Failed++
			}
			p.mu.Unlock()
		})
	}
	// The outbox sends 20 a second; queueing everyone at once would keep a message and a
	// closure per account in memory for as long as that takes. A first batch goes in now,
	// the rest as the lane drains.
	first := min(len(targets), bulkBatch)
	for _, chat := range targets[:first] {
		queue(chat)
	}
	if rest := targets[first:]; len(rest) > 0 {
		go p.feed(out, rest, queue)
	}
	return len(targets), nil
}

// bulkBatch is how many broadcast messages wait in the outbox; feed tops the lane up when
// fewer than bulkLow are left.
const (
	bulkBatch = 500
	bulkLow   = 100
)

// feed queues the rest of a broadcast in batches as the outbox sends. If the outbox stops
// (the bot is reloaded or switched off) what is left is counted as failed, so the
// progress ends.
func (p *Panel) feed(out *tgate.Outbox, rest []int64, queue func(chat int64)) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for len(rest) > 0 {
		select {
		case <-out.Stopped():
			p.mu.Lock()
			p.bcast.Failed += len(rest)
			p.mu.Unlock()
			return
		case <-t.C:
		}
		if out.BulkPending() > bulkLow {
			continue
		}
		n := min(len(rest), bulkBatch)
		for _, chat := range rest[:n] {
			queue(chat)
		}
		rest = rest[n:]
	}
}

// Progress is the last broadcast.
func (p *Panel) Progress() BroadcastProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bcast
}
