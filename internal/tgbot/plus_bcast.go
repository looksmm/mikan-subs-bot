package tgbot

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"subsbot/internal/plus"
	"subsbot/internal/tgate"
)

// The admin's broadcasts (plus.Broadcast): one message to everyone the bot knows, or to a
// part of them, sent in the background at the bulk pace after the replies and the notices.
// mikan's own broadcast reaches only the accounts linked to a subscription; this one reaches
// every chat that ever wrote to the bot too. One job at a time; it is kept in the panel's
// store and goes on after a restart.

var (
	ErrBroadcastBusy  = errors.New("broadcast_busy")
	ErrBroadcastNone  = errors.New("broadcast_none")
	ErrBroadcastUsers = errors.New("broadcast_users") // the audience needs users:all
	ErrBroadcastEmpty = errors.New("broadcast_empty")
)

// bulkInFlight: how many messages of a broadcast wait in the outbox at most.
const bulkInFlight = 200

// bcast is the runner's state.
type bcast struct {
	once   sync.Once
	mu     sync.Mutex
	wake   chan struct{}
	cancel atomic.Bool
}

func (b *Bot) bc() *bcast {
	r := &b.x.bc
	r.once.Do(func() { r.wake = make(chan struct{}, 1) })
	return r
}

func (r *bcast) poke() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Audience is who a broadcast to key would reach now: their accounts, in order.
func (b *Bot) Audience(key string) ([]int64, error) {
	e := b.x.e
	aud, ok := plus.AudienceOf(key)
	if !ok {
		return nil, ErrBroadcastNone
	}
	var byTg map[int64][]plus.Sub
	usersOK := false
	if e.Users != nil {
		var at time.Time
		var err error
		byTg, at, err = e.Users.Snapshot()
		usersOK = err == nil && !at.IsZero()
	}
	if aud.Users && !usersOK {
		return nil, ErrBroadcastUsers
	}
	seen := map[int64]bool{}
	var out []int64
	add := func(tg int64) {
		if seen[tg] {
			return
		}
		seen[tg] = true
		subs := byTg[tg]
		switch key {
		case "active":
			if !plus.Has(subs) {
				return
			}
		case "expired":
			if len(subs) == 0 || plus.Has(subs) {
				return
			}
		case "none":
			if len(subs) > 0 {
				return
			}
		}
		out = append(out, tg)
	}
	blocked := map[int64]bool{}
	e.Chats.Each(func(tg int64, ch plus.Chat) {
		if ch.Blocked != 0 {
			blocked[tg] = true
			return
		}
		add(tg)
	})
	for tg := range byTg {
		if !blocked[tg] {
			add(tg)
		}
	}
	slices.Sort(out)
	return out, nil
}

// AudienceCounts is how many each audience reaches now; usersOK says whether the panel's
// subscribers could be read (else only "all" is told).
func (b *Bot) AudienceCounts() (map[string]int, bool) {
	out := map[string]int{}
	ok := true
	for _, a := range plus.Audiences {
		to, err := b.Audience(a.Key)
		if err != nil {
			ok = false
			continue
		}
		out[a.Key] = len(to)
	}
	return out, ok
}

// StartBroadcast starts j now, or schedules it for j.At.
func (b *Bot) StartBroadcast(ctx context.Context, j plus.Broadcast) (*plus.Broadcast, error) {
	if b.x == nil || b.x.e.Broadcasts == nil || b.x.e.Chats == nil {
		return nil, ErrBroadcastNone
	}
	if err := j.Valid(); err != nil {
		return nil, err
	}
	r := b.bc()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := b.x.e.Broadcasts
	cur, err := st.Job(ctx)
	if err != nil {
		return nil, err
	}
	if cur.Active() {
		return nil, ErrBroadcastBusy
	}
	now := b.d.Now()
	j.ID, j.Created = plus.NewID(now.UnixNano()/1e6), now.Unix()
	j.Sent, j.Failed, j.Blocked, j.Next, j.Total, j.Chunks, j.Started, j.Finished, j.Error = 0, 0, 0, 0, 0, 0, 0, 0, ""
	if j.At > now.Unix()+30 {
		// Who gets it is told when it starts: the list then, not now.
		if _, err := b.Audience(j.Audience); err != nil {
			return nil, err
		}
		j.State = plus.BcastScheduled
	} else {
		j.At = 0
		if err := b.begin(ctx, &j); err != nil {
			return nil, err
		}
	}
	if err := st.Save(ctx, &j); err != nil {
		return nil, err
	}
	r.cancel.Store(false)
	r.poke()
	b.d.Log.Info("broadcast", "id", j.ID, "audience", j.Audience, "state", j.State, "total", j.Total)
	return &j, nil
}

// begin writes j's recipients and makes it run.
func (b *Bot) begin(ctx context.Context, j *plus.Broadcast) error {
	to, err := b.Audience(j.Audience)
	if err != nil {
		return err
	}
	if len(to) == 0 {
		return ErrBroadcastEmpty
	}
	if err := b.x.e.Broadcasts.SetRecipients(ctx, j, to); err != nil {
		return err
	}
	j.State, j.Started = plus.BcastRunning, b.d.Now().Unix()
	return nil
}

// CancelBroadcast stops the current job: what is already in the outbox still goes.
func (b *Bot) CancelBroadcast(ctx context.Context) (*plus.Broadcast, error) {
	r := b.bc()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := b.x.e.Broadcasts
	j, err := st.Job(ctx)
	if err != nil {
		return nil, err
	}
	if !j.Active() {
		return j, nil
	}
	r.cancel.Store(true)
	if j.State == plus.BcastScheduled {
		j.State, j.Finished = plus.BcastCancelled, b.d.Now().Unix()
		return j, st.Done(ctx, j)
	}
	r.poke()
	return j, nil
}

// BroadcastRun runs the jobs until ctx ends.
func (b *Bot) BroadcastRun(ctx context.Context) {
	if b.x == nil || b.x.e.Broadcasts == nil || b.x.e.Chats == nil {
		return
	}
	r := b.bc()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		if err := b.bcastStep(ctx); err != nil && ctx.Err() == nil {
			b.x.auto.warn(b.d.Log, "broadcast: the job", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
		}
	}
}

// bcastStep starts a scheduled job whose time came and sends a running one to its end.
func (b *Bot) bcastStep(ctx context.Context) error {
	if !b.x.e.Chats.Loaded() {
		return nil
	}
	st := b.x.e.Broadcasts
	r := b.bc()
	r.mu.Lock()
	j, err := st.Job(ctx)
	if err != nil || !j.Active() {
		r.mu.Unlock()
		return err
	}
	if j.State == plus.BcastScheduled {
		if b.d.Now().Unix() < j.At {
			r.mu.Unlock()
			return nil
		}
		if err := b.begin(ctx, j); err != nil {
			j.State, j.Finished, j.Error = plus.BcastCancelled, b.d.Now().Unix(), err.Error()
			r.mu.Unlock()
			return st.Done(ctx, j)
		}
		if err := st.Save(ctx, j); err != nil {
			r.mu.Unlock()
			return err
		}
	}
	r.mu.Unlock()
	return b.sendJob(ctx, j)
}

// sendJob sends a running job from where it stopped. A restart resumes from the messages
// that are done: one in the outbox when it stopped may go twice, none is lost.
func (b *Bot) sendJob(ctx context.Context, j *plus.Broadcast) error {
	st := b.x.e.Broadcasts
	r := b.bc()
	pm := b.prepareMsg(ctx, j.Message)
	w := wordsFor(b.config(ctx).Lang)
	var byTg map[int64][]plus.Sub
	if b.x.e.Users != nil {
		byTg, _, _ = b.x.e.Users.Snapshot()
	}
	var sent, failed, blocked, inFlight atomic.Int64
	sent.Store(int64(j.Sent))
	failed.Store(int64(j.Failed))
	blocked.Store(int64(j.Blocked))
	done := func(err error) {
		var ae *tgate.APIError
		switch {
		case err == nil:
			sent.Add(1)
		case errors.As(err, &ae) && ae.Code == 403:
			blocked.Add(1)
		default:
			failed.Add(1)
		}
		inFlight.Add(-1)
	}
	save := func() {
		j.Sent, j.Failed, j.Blocked = int(sent.Load()), int(failed.Load()), int(blocked.Load())
		_ = st.Save(ctx, j)
	}
	lastSave := time.Now()
	start := j.Sent + j.Failed + j.Blocked
	for n := start / plus.BcastChunk; n < j.Chunks && !r.cancel.Load() && ctx.Err() == nil; n++ {
		to, err := st.Recipients(ctx, j, n)
		if err != nil {
			return err
		}
		for i, tg := range to {
			idx := n*plus.BcastChunk + i
			if idx < start {
				continue
			}
			for inFlight.Load() >= bulkInFlight && ctx.Err() == nil && !r.cancel.Load() {
				time.Sleep(100 * time.Millisecond)
				if time.Since(lastSave) > 2*time.Second {
					save()
					lastSave = time.Now()
				}
			}
			if ctx.Err() != nil || r.cancel.Load() {
				break
			}
			sub := pickSub(byTg[tg])
			inFlight.Add(1)
			j.Next = idx + 1
			b.queueAdmin(tg, pm, b.msgVars(w, pm.brand, tg, sub), sub != nil && sub.Works(), "bulk", done)
		}
	}
	// What is in the outbox goes out before the job ends.
	for inFlight.Load() > 0 && ctx.Err() == nil {
		select {
		case <-b.d.Outbox.Stopped():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
		if time.Since(lastSave) > 2*time.Second {
			save()
			lastSave = time.Now()
		}
	}
	if ctx.Err() != nil {
		save()
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	j.Sent, j.Failed, j.Blocked = int(sent.Load()), int(failed.Load()), int(blocked.Load())
	j.State, j.Finished = plus.BcastDone, b.d.Now().Unix()
	if r.cancel.Load() {
		j.State = plus.BcastCancelled
	}
	b.d.Log.Info("broadcast: done", "id", j.ID, "state", j.State, "sent", j.Sent, "failed", j.Failed, "blocked", j.Blocked)
	return st.Done(ctx, j)
}

// pickSub is the subscription a message to an account is about: one that works, else the
// first; nil when there is none.
func pickSub(subs []plus.Sub) *plus.Sub {
	for i := range subs {
		if subs[i].Works() {
			return &subs[i]
		}
	}
	if len(subs) > 0 {
		return &subs[0]
	}
	return nil
}
