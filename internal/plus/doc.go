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

// Doc is one JSON value of the panel's store kept in memory: the menu, the automatic
// messages. Read at start and every refreshEvery, written by Save; until the first read it
// is the default.
type Doc[T any] struct {
	kv   *KV
	key  string
	log  *slog.Logger
	def  func() T
	norm func(*T)
	// valid checks what is saved; prev is what is saved now (rules keep their creation time).
	valid func(next, prev *T) error

	cur    atomic.Pointer[T]
	loaded atomic.Bool
	save   sync.Mutex
}

func newDoc[T any](kv *KV, key string, log *slog.Logger, def func() T, norm func(*T), valid func(next, prev *T) error) *Doc[T] {
	d := &Doc[T]{kv: kv, key: key, log: log, def: def, norm: norm, valid: valid}
	v := def()
	d.cur.Store(&v)
	return d
}

// NewMenu is the admin's menu in the panel's store.
func NewMenu(kv *KV, log *slog.Logger) *Doc[Menu] {
	return newDoc(kv, menuKey, log, DefaultMenu, (*Menu).Normalize, func(next, _ *Menu) error { return next.Valid() })
}

// NewAuto is the admin's automatic messages in the panel's store. A rule saved for the
// first time is stamped with the time: it acts only on what happens from then on.
func NewAuto(kv *KV, log *slog.Logger, now func() time.Time) *Doc[Auto] {
	return newDoc(kv, autoKey, log, DefaultAuto, (*Auto).Normalize, func(next, prev *Auto) error {
		if err := next.Valid(); err != nil {
			return err
		}
		for i := range next.Rules {
			r := &next.Rules[i]
			if old, ok := prev.Rule(r.ID); ok && old.Created > 0 {
				r.Created = old.Created
			} else {
				r.Created = now().Unix()
			}
		}
		return nil
	})
}

// Get is the value as last read.
func (d *Doc[T]) Get() *T { return d.cur.Load() }

// Loaded says whether the value was read from the panel at least once.
func (d *Doc[T]) Loaded() bool { return d.loaded.Load() }

// Load reads the value from the panel; none saved yet is the default.
func (d *Doc[T]) Load(ctx context.Context) error {
	b, _, err := d.kv.Get(ctx, d.key)
	next := d.def()
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(b, &next); err != nil {
			return err
		}
	}
	d.norm(&next)
	d.cur.Store(&next)
	d.loaded.Store(true)
	return nil
}

// Run reads the value now and every refreshEvery until ctx ends.
func (d *Doc[T]) Run(ctx context.Context) {
	told := false
	for {
		if err := d.Load(ctx); err != nil && ctx.Err() == nil {
			if !told {
				d.log.Warn("settings: read", "key", d.key, "err", err)
				told = true
			}
		} else {
			told = false
		}
		wait := refreshEvery
		if !d.loaded.Load() {
			wait = 3 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Save checks next and writes it; stamp sets its time of saving.
func (d *Doc[T]) Save(ctx context.Context, next T, stamp func(*T, int64)) (*T, error) {
	d.save.Lock()
	defer d.save.Unlock()
	d.norm(&next)
	if err := d.valid(&next, d.Get()); err != nil {
		return nil, err
	}
	if stamp != nil {
		stamp(&next, time.Now().Unix())
	}
	b, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	if len(b) > 64<<10 {
		return nil, &FieldError{"", "слишком много текста: в панели одно значение — до 64 КБ, сократите тексты"}
	}
	if _, err := d.kv.Put(ctx, d.key, b, 0, 0); err != nil {
		return nil, err
	}
	d.cur.Store(&next)
	d.loaded.Store(true)
	return &next, nil
}

// Use puts v in memory as if it had been read, without writing it (tests).
func (d *Doc[T]) Use(v T) {
	d.norm(&v)
	d.cur.Store(&v)
	d.loaded.Store(true)
}
