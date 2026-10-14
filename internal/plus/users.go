package plus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The panel's subscribers (Panel API /users, permission users:all): who has a subscription
// that works, who had one, and until when. The bot reads them all every few minutes for the
// broadcasts and the automatic messages; the panel answers only with what an addon may see
// (no links, no notes).

// Sub is a subscription as the list tells it.
type Sub struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	State      string     `json:"state"` // active, expiring, limited, expired, disabled
	TariffID   *int64     `json:"tariff_id"`
	TariffName string     `json:"tariff_name"`
	ExpiresAt  *time.Time `json:"expires_at"`
	TgID       *int64     `json:"tg_id"`
}

// Works: the subscription serves its user now (out of traffic still counts: it is theirs).
func (s Sub) Works() bool {
	return s.State == "active" || s.State == "expiring" || s.State == "limited"
}

// ErrNoUsersAll: the admin did not give the addon the permission users:all.
var ErrNoUsersAll = errors.New("the addon has no permission users:all")

// Users reads the panel's subscribers.
type Users struct {
	base, token string
	hc          *http.Client
	log         *slog.Logger

	mu      sync.Mutex
	byTg    map[int64][]Sub
	at      time.Time
	err     error
	reading bool
}

func NewUsers(base, token string, log *slog.Logger) *Users {
	return &Users{base: strings.TrimRight(base, "/"), token: token, hc: &http.Client{Timeout: 20 * time.Second}, log: log}
}

func (u *Users) get(ctx context.Context, path string, q url.Values, out any) error {
	ref := u.base + path
	if len(q) > 0 {
		ref += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+u.token)
	resp, err := u.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return json.Unmarshal(b, out)
	case http.StatusForbidden:
		return ErrNoUsersAll
	case http.StatusNotFound:
		return ErrNotFound
	}
	return fmt.Errorf("users: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

type usersPage struct {
	Users []Sub  `json:"users"`
	Next  *int64 `json:"next"`
}

// All reads every subscriber, a page of 100 at a time.
func (u *Users) All(ctx context.Context) ([]Sub, error) {
	var out []Sub
	after := int64(0)
	for range 10000 {
		q := url.Values{"limit": {"100"}}
		if after > 0 {
			q.Set("after", strconv.FormatInt(after, 10))
		}
		var p usersPage
		if err := u.get(ctx, "/users", q, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Users...)
		if p.Next == nil {
			return out, nil
		}
		after = *p.Next
	}
	return out, errors.New("users: the list does not end")
}

// OfTg reads the subscriptions linked to the Telegram account tg, now.
func (u *Users) OfTg(ctx context.Context, tg int64) ([]Sub, error) {
	var out []Sub
	after := int64(0)
	for range 50 {
		q := url.Values{"q": {strconv.FormatInt(tg, 10)}, "limit": {"100"}}
		if after > 0 {
			q.Set("after", strconv.FormatInt(after, 10))
		}
		var p usersPage
		if err := u.get(ctx, "/users", q, &p); err != nil {
			return nil, err
		}
		for _, s := range p.Users {
			// The search matches a name or an id too: only the linked ones count.
			if s.TgID != nil && *s.TgID == tg {
				out = append(out, s)
			}
		}
		if p.Next == nil {
			break
		}
		after = *p.Next
	}
	return out, nil
}

// One reads the subscription id.
func (u *Users) One(ctx context.Context, id int64) (Sub, error) {
	var s Sub
	err := u.get(ctx, "/users/"+strconv.FormatInt(id, 10), nil, &s)
	return s, err
}

// Refresh reads the whole list now.
func (u *Users) Refresh(ctx context.Context) error {
	u.mu.Lock()
	if u.reading {
		u.mu.Unlock()
		return nil
	}
	u.reading = true
	u.mu.Unlock()
	all, err := u.All(ctx)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.reading = false
	u.err = err
	if err != nil {
		return err
	}
	byTg := map[int64][]Sub{}
	for _, s := range all {
		if s.TgID != nil && *s.TgID > 0 {
			byTg[*s.TgID] = append(byTg[*s.TgID], s)
		}
	}
	u.byTg, u.at = byTg, time.Now()
	return nil
}

// Snapshot is the last list read, by Telegram account, and when it was read; at is zero
// when it never was.
func (u *Users) Snapshot() (byTg map[int64][]Sub, at time.Time, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.byTg, u.at, u.err
}

// Run reads the list now and every `every` until ctx ends; on reads hands it to seed.
func (u *Users) Run(ctx context.Context, every time.Duration, seed func(map[int64][]Sub)) {
	told := ""
	for {
		err := u.Refresh(ctx)
		switch {
		case err == nil:
			told = ""
			if seed != nil {
				byTg, _, _ := u.Snapshot()
				seed(byTg)
			}
		case ctx.Err() != nil:
		case err.Error() != told:
			told = err.Error()
			if errors.Is(err, ErrNoUsersAll) {
				u.log.Info("subscribers: the addon has no permission «Все пользователи»: broadcasts reach only the chats the bot met")
			} else {
				u.log.Warn("subscribers: read", "err", err)
			}
		}
		wait := every
		if err != nil && !errors.Is(err, ErrNoUsersAll) {
			wait = min(every, time.Minute)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Has: tg has a subscription that works, as of the last list read.
func Has(subs []Sub) bool {
	for _, s := range subs {
		if s.Works() {
			return true
		}
	}
	return false
}

// Self is what the panel takes the addon for: its id and the permissions the admin gave.
type Self struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Permissions []string `json:"permissions"`
}

// Self asks the panel who the addon is to it.
func (u *Users) Self(ctx context.Context) (Self, error) {
	var s Self
	err := u.get(ctx, "/self", nil, &s)
	return s, err
}
