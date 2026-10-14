package plus

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// settingsKey is where the settings live in the panel's store.
const settingsKey = "settings"

// refreshEvery: how often the settings are read again, for a save made by another run of
// the addon (after a restart there is one at a time, but the page and the bot share them).
const refreshEvery = 30 * time.Second

// Store keeps the settings: in the panel's store, and the last read in memory for the bot,
// which dresses every message with them.
type Store struct {
	kv  *KV
	log *slog.Logger

	cur    atomic.Pointer[Settings]
	loaded atomic.Bool
	save   sync.Mutex // one save at a time: a save reads, merges and writes
	// check is what the bot's side says of settings to save (its texts), nil nothing.
	check func(*Settings) error
}

// Check adds the bot's own checks of settings to save; set it before the page saves.
func (s *Store) Check(f func(*Settings) error) { s.check = f }

func NewStore(kv *KV, log *slog.Logger) *Store {
	s := &Store{kv: kv, log: log}
	d := Default()
	s.cur.Store(&d)
	return s
}

// Get is the settings as last read; the defaults until the first read.
func (s *Store) Get() *Settings { return s.cur.Load() }

// Loaded says whether the settings were read from the panel at least once.
func (s *Store) Loaded() bool { return s.loaded.Load() }

// Load reads the settings from the panel; none saved yet is the defaults.
func (s *Store) Load(ctx context.Context) error {
	b, _, err := s.kv.Get(ctx, settingsKey)
	if errors.Is(err, ErrNotFound) {
		d := Default()
		s.cur.Store(&d)
		s.loaded.Store(true)
		return nil
	}
	if err != nil {
		return err
	}
	var next Settings
	if err := json.Unmarshal(b, &next); err != nil {
		return err
	}
	next.Normalize()
	s.cur.Store(&next)
	s.loaded.Store(true)
	return nil
}

// Run reads the settings now and every refreshEvery until ctx ends.
func (s *Store) Run(ctx context.Context) {
	told := false
	for {
		if err := s.Load(ctx); err != nil && ctx.Err() == nil {
			if !told {
				s.log.Warn("settings: read", "err", err)
				told = true
			}
		} else {
			told = false
		}
		t := s.refresh()
		select {
		case <-ctx.Done():
			return
		case <-time.After(t):
		}
	}
}

func (s *Store) refresh() time.Duration {
	if !s.loaded.Load() {
		return 3 * time.Second
	}
	return refreshEvery
}

// Save writes next as the settings, checked first.
func (s *Store) Save(ctx context.Context, next Settings) (*Settings, error) {
	s.save.Lock()
	defer s.save.Unlock()
	next.Normalize()
	if err := next.Valid(); err != nil {
		return nil, err
	}
	if s.check != nil {
		if err := s.check(&next); err != nil {
			return nil, err
		}
	}
	next.Saved = time.Now().Unix()
	b, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	if _, err := s.kv.Put(ctx, settingsKey, b, 0, 0); err != nil {
		return nil, err
	}
	s.cur.Store(&next)
	s.loaded.Store(true)
	return &next, nil
}

// Use puts next in memory as if it had been read from the panel, without writing it.
func (s *Store) Use(next Settings) {
	next.Normalize()
	s.cur.Store(&next)
	s.loaded.Store(true)
}
