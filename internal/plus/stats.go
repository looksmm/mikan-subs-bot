package plus

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"sync"
	"time"
)

// How the admin's messages do: how many went out, and how many presses their buttons got.
// Kept in the panel's store (key "stats"), counted in memory and written every half minute.
// Keys: "r.<rule>" an automatic message, "m.<key>" one of the bot's own, "b.<job>" a broadcast.

const (
	statsKey   = "stats"
	statsEvery = 30 * time.Second
)

// Count is one message's: sent, and presses of its buttons (links are not counted: Telegram
// does not tell a bot of them).
type Count struct {
	Sent    int64 `json:"sent"`
	Presses int64 `json:"presses"`
	Since   int64 `json:"since"` // when the count began (unix)
}

// Stats are the counts.
type Stats struct {
	kv  *KV
	log *slog.Logger
	now func() time.Time

	mu     sync.Mutex
	counts map[string]Count
	dirty  bool
	loaded bool
}

func NewStats(kv *KV, log *slog.Logger, now func() time.Time) *Stats {
	return &Stats{kv: kv, log: log, now: now, counts: map[string]Count{}}
}

func (s *Stats) add(key string, sent, presses int64) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.counts[key]
	if c.Since == 0 {
		c.Since = s.now().Unix()
	}
	c.Sent += sent
	c.Presses += presses
	s.counts[key] = c
	s.dirty = true
}

// Sent counts a message that went out.
func (s *Stats) Sent(key string) { s.add(key, 1, 0) }

// Pressed counts a press of a message's button.
func (s *Stats) Pressed(key string) { s.add(key, 0, 1) }

// All is the counts now.
func (s *Stats) All() map[string]Count {
	if s == nil {
		return map[string]Count{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.counts)
}

// Forget drops the counts keep says no to (rules that are gone).
func (s *Stats) Forget(keep func(key string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.counts {
		if !keep(k) {
			delete(s.counts, k)
			s.dirty = true
		}
	}
}

// Load reads the counts and adds what was counted meanwhile.
func (s *Stats) Load(ctx context.Context) error {
	b, _, err := s.kv.Get(ctx, statsKey)
	stored := map[string]Count{}
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(b, &stored); err != nil {
			s.log.Warn("stats: the stored counts do not read, they start over", "err", err)
			stored = map[string]Count{}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, c := range s.counts {
		st := stored[k]
		st.Sent += c.Sent
		st.Presses += c.Presses
		if st.Since == 0 {
			st.Since = c.Since
		}
		stored[k] = st
	}
	s.counts, s.loaded = stored, true
	return nil
}

// Flush writes the counts when they changed.
func (s *Stats) Flush(ctx context.Context) {
	s.mu.Lock()
	if !s.loaded || !s.dirty {
		s.mu.Unlock()
		return
	}
	b, err := json.Marshal(s.counts)
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return
	}
	if _, err := s.kv.Put(ctx, statsKey, b, 0, 0); err != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
		if ctx.Err() == nil {
			s.log.Warn("stats: write", "err", err)
		}
	}
}

// Run reads the counts, then writes them every half minute until ctx ends, and once more then.
func (s *Stats) Run(ctx context.Context) {
	for wait := time.Second; ctx.Err() == nil; wait = min(2*wait, time.Minute) {
		if err := s.Load(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	t := time.NewTicker(statsEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.Flush(fctx)
			cancel()
			return
		case <-t.C:
			s.Flush(ctx)
		}
	}
}
