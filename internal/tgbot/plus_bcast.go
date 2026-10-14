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
	// ErrNotHolder: the admin has not given the bot to this addon: Telegram is not its to use.
	ErrNotHolder = errors.New("not_holder")
)

// holderTTL: how long the answer of whether the bot is this addon's is kept.
const holderTTL = 20 * time.Second

// Holds says whether the panel gave the bot to this addon: the Bot API proxy answers
// everyone else 401. Asked at most every holderTTL.
func (b *Bot) Holds(ctx context.Context) bool {
	r := b.bc()
	r.mu.Lock()
	if time.Since(r.holdAt) < holderTTL {
		h := r.holds
		r.mu.Unlock()
		return h
	}
	r.mu.Unlock()
	_, err := b.d.Client.Me(ctx)
	var ae *tgate.APIError
	h := !(errors.As(err, &ae) && ae.Code == 401)
	if err != nil && h {
		return true // the way to the panel failed: no news about who holds the bot
	}
	r.mu.Lock()
	r.holds, r.holdAt = h, time.Now()
	r.mu.Unlock()
	return h
}

// notHolderErr: a send the proxy refused because the bot is another addon's.
func notHolderErr(err error) bool {
	var ae *tgate.APIError
	return errors.As(err, &ae) && ae.Code == 401
}

// bulkInFlight: how many messages of a broadcast wait in the outbox at most.
const bulkInFlight = 200

// bcast is the runner's state.
type bcast struct {
	once   sync.Once
	mu     sync.Mutex
	wake   chan struct{}
	cancel atomic.Bool
	holds  bool      // the bot is this addon's, as last asked
	holdAt time.Time // when that was asked
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
	if !b.Holds(ctx) {
		return nil, ErrNotHolder
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

// progress is which recipients of a job are done: everyone below next, and those in ahead.
type progress struct {
	mu    sync.Mutex
	next  int
	ahead map[int]bool
}

func newProgress(j *plus.Broadcast) *progress {
	p := &progress{next: j.Next, ahead: map[int]bool{}}
	done := j.Sent + j.Failed + j.Blocked
	if len(j.Ahead) == 0 && p.next > done {
		p.next = done // a job of 1.0.1: Next counted what was queued, not what was done
	}
	for _, i := range j.Ahead {
		if i >= p.next {
			p.ahead[i] = true
		}
	}
	return p
}

func (p *progress) done(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < p.next {
		return
	}
	p.ahead[i] = true
	for p.ahead[p.next] {
		delete(p.ahead, p.next)
		p.next++
	}
}

func (p *progress) isDone(i int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return i < p.next || p.ahead[i]
}

// put writes the progress into the job.
func (p *progress) put(j *plus.Broadcast) {
	p.mu.Lock()
	defer p.mu.Unlock()
	j.Next = p.next
	j.Ahead = j.Ahead[:0]
	for i := range p.ahead {
		j.Ahead = append(j.Ahead, i)
	}
	slices.Sort(j.Ahead)
}

// sendJob sends what is left of a running job. The job's progress is saved every few
// seconds; a restart may send again the few messages that went out after the last save,
// a pause (the bot given to another addon) sends nobody twice. While the bot is another
// addon's the job waits: the proxy would refuse every message.
func (b *Bot) sendJob(ctx context.Context, j *plus.Broadcast) error {
	if !b.Holds(ctx) {
		return nil
	}
	st := b.x.e.Broadcasts
	r := b.bc()
	pm := b.prepareMsg(ctx, j.Message)
	w := wordsFor(b.config(ctx).Lang)
	var byTg map[int64][]plus.Sub
	if b.x.e.Users != nil {
		byTg, _, _ = b.x.e.Users.Snapshot()
	}
	prog := newProgress(j)
	var sent, failed, blocked, inFlight atomic.Int64
	var lost atomic.Bool // the proxy refused a message: the bot was given to another addon
	sent.Store(int64(j.Sent))
	failed.Store(int64(j.Failed))
	blocked.Store(int64(j.Blocked))
	doneOf := func(idx int) func(error) {
		return func(err error) {
			var ae *tgate.APIError
			switch {
			case err == nil:
				sent.Add(1)
			case notHolderErr(err):
				// Not done: it goes again once the bot is this addon's.
				lost.Store(true)
				inFlight.Add(-1)
				return
			case errors.As(err, &ae) && ae.Code == 403:
				blocked.Add(1)
			default:
				failed.Add(1)
			}
			prog.done(idx)
			inFlight.Add(-1)
		}
	}
	save := func() {
		j.Sent, j.Failed, j.Blocked = int(sent.Load()), int(failed.Load()), int(blocked.Load())
		prog.put(j)
		_ = st.Save(ctx, j)
	}
	lastSave := time.Now()
	stop := func() bool { return ctx.Err() != nil || r.cancel.Load() || lost.Load() }
	for n := prog.next / plus.BcastChunk; n < j.Chunks && !stop(); n++ {
		to, err := st.Recipients(ctx, j, n)
		if err != nil {
			return err
		}
		for i, tg := range to {
			idx := n*plus.BcastChunk + i
			if prog.isDone(idx) {
				continue
			}
			for inFlight.Load() >= bulkInFlight && !stop() {
				time.Sleep(100 * time.Millisecond)
				if time.Since(lastSave) > 2*time.Second {
					save()
					lastSave = time.Now()
				}
			}
			if stop() {
				break
			}
			sub := pickSub(byTg[tg])
			inFlight.Add(1)
			b.queueAdmin(tg, pm, b.msgVars(w, pm.brand, tg, sub), sub != nil && sub.Works(), "bulk", doneOf(idx))
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
	if lost.Load() && !r.cancel.Load() {
		// Paused: the job stays running and resumes with the rest once the bot is back.
		save()
		r.mu.Lock()
		r.holdAt = time.Time{}
		r.mu.Unlock()
		b.d.Log.Warn("broadcast: paused, the bot is another addon's", "id", j.ID, "done", j.Sent+j.Failed+j.Blocked, "total", j.Total)
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	save()
	j.State, j.Finished = plus.BcastDone, b.d.Now().Unix()
	if r.cancel.Load() {
		j.State = plus.BcastCancelled
	}
	j.Ahead = nil
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
