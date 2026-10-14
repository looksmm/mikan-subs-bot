package plus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Chats are everyone who wrote to the bot, as the bot saw them: the panel keeps only the
// accounts linked to a subscription, and a broadcast or a message to those who never bought
// needs the rest. They live in the panel's store, split by the account's id into
// chatShards values, and in memory; what changed goes to the panel every ten seconds.

const (
	chatsPrefix = "chats/"
	chatShards  = 256
	flushEvery  = 10 * time.Second // a wave of /start dirties every part: ~25 writes a second at most
	maxChatName = 32
)

// Chat is one account the bot knows.
type Chat struct {
	// First is when the bot saw it first (unix); 0 is an account known only from the panel's
	// subscriptions, from before the bot kept its chats.
	First int64 `json:"f,omitempty"`
	// Seen is its last message or tap.
	Seen int64 `json:"l,omitempty"`
	// Start: its first message was /start: a newcomer, not an old customer the bot did not
	// know yet.
	Start bool `json:"st,omitempty"`
	// Blocked is when Telegram said it blocked the bot; a message of its own clears it.
	Blocked int64  `json:"b,omitempty"`
	Name    string `json:"n,omitempty"`
	// Sent: the automatic messages it got, by rule: when (or the event they followed).
	Sent map[string]int64 `json:"s,omitempty"`
	// Due are automatic messages waiting for their time.
	Due []Due `json:"d,omitempty"`
}

// Due is an automatic message to send at At: its rule, the subscription and the event it
// is about.
type Due struct {
	Rule  string `json:"r"`
	At    int64  `json:"t"`
	User  int64  `json:"u,omitempty"`
	Event int64  `json:"e,omitempty"`
}

// DueItem is a Due taken for sending, with its account.
type DueItem struct {
	TgID int64
	Due
}

type chatShard struct {
	ver   int64 // the stored version; 0 none stored yet
	chats map[int64]*Chat
}

// Chats is the bot's list of accounts.
type Chats struct {
	kv  *KV
	log *slog.Logger
	now func() time.Time

	mu     sync.Mutex
	shards [chatShards]chatShard
	dirty  map[int]bool
	loaded bool
	// full: a shard the panel would not take (64 KiB): told once.
	full map[int]bool
}

// NewChats is the list in the panel's store.
func NewChats(kv *KV, log *slog.Logger, now func() time.Time) *Chats {
	c := &Chats{kv: kv, log: log, now: now, dirty: map[int]bool{}, full: map[int]bool{}}
	for i := range c.shards {
		c.shards[i].chats = map[int64]*Chat{}
	}
	return c
}

func shardOf(tg int64) int {
	v := tg % chatShards
	if v < 0 {
		v = -v
	}
	return int(v)
}

func shardKey(i int) string { return fmt.Sprintf("%s%02x", chatsPrefix, i) }

func (c *Chats) chat(tg int64, create bool) *Chat {
	s := &c.shards[shardOf(tg)]
	ch := s.chats[tg]
	if ch == nil && create {
		ch = &Chat{}
		s.chats[tg] = ch
	}
	return ch
}

func (c *Chats) touch(tg int64) { c.dirty[shardOf(tg)] = true }

// Loaded says whether the list was read from the panel.
func (c *Chats) Loaded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loaded
}

// Seen records a message or a tap of tg; start says it was /start. It answers whether the
// account is new to the bot.
func (c *Chats) Seen(tg int64, name string, start bool) bool {
	if tg <= 0 {
		return false
	}
	now := c.now().Unix()
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.chat(tg, false)
	isNew := ch == nil
	if isNew {
		ch = c.chat(tg, true)
		ch.First, ch.Start = now, start
	} else if ch.First == 0 {
		// Known from its subscription only: an old customer, never a newcomer.
		ch.First = now
	}
	// A minute's precision is enough, and saves the panel a write per tap.
	if now-ch.Seen >= 60 || isNew || ch.Blocked != 0 {
		ch.Seen = now
		ch.Blocked = 0
		c.touch(tg)
	}
	if name = cut(name, maxChatName); name != "" && name != ch.Name {
		ch.Name = name
		c.touch(tg)
	}
	return isNew
}

// Blocked records that tg blocked the bot.
func (c *Chats) Blocked(tg int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.chat(tg, true)
	if ch.Blocked == 0 {
		ch.Blocked = c.now().Unix()
		c.touch(tg)
	}
}

// Seed adds the accounts of the panel's subscriptions the bot has not met: a broadcast
// reaches them too, and they are no newcomers.
func (c *Chats) Seed(tgs []int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, tg := range tgs {
		if tg > 0 && c.chat(tg, false) == nil {
			c.chat(tg, true)
			c.touch(tg)
			n++
		}
	}
	return n
}

// Get is what the bot knows of tg.
func (c *Chats) Get(tg int64) (Chat, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.chat(tg, false)
	if ch == nil {
		return Chat{}, false
	}
	return ch.copy(), true
}

func (ch *Chat) copy() Chat {
	out := *ch
	if ch.Sent != nil {
		out.Sent = make(map[string]int64, len(ch.Sent))
		for k, v := range ch.Sent {
			out.Sent[k] = v
		}
	}
	out.Due = slices.Clone(ch.Due)
	return out
}

// Each calls f for every account, on copies: f may take its time.
func (c *Chats) Each(f func(tg int64, ch Chat)) {
	type item struct {
		tg int64
		ch Chat
	}
	c.mu.Lock()
	all := make([]item, 0, 1024)
	for i := range c.shards {
		for tg, ch := range c.shards[i].chats {
			all = append(all, item{tg, ch.copy()})
		}
	}
	c.mu.Unlock()
	slices.SortFunc(all, func(a, b item) int {
		switch {
		case a.tg < b.tg:
			return -1
		case a.tg > b.tg:
			return 1
		}
		return 0
	})
	for _, it := range all {
		f(it.tg, it.ch)
	}
}

// Counts: accounts in all, and those that blocked the bot.
func (c *Chats) Counts() (total, blocked int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.shards {
		for _, ch := range c.shards[i].chats {
			total++
			if ch.Blocked != 0 {
				blocked++
			}
		}
	}
	return total, blocked
}

// Sent is what the rule's last message to tg was for (0: none).
func (c *Chats) Sent(tg int64, rule string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch := c.chat(tg, false); ch != nil {
		return ch.Sent[rule]
	}
	return 0
}

// MarkSent records the rule's message to tg, for v (a time or an event).
func (c *Chats) MarkSent(tg int64, rule string, v int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.chat(tg, true)
	if ch.Sent == nil {
		ch.Sent = map[string]int64{}
	}
	ch.Sent[rule] = v
	c.touch(tg)
}

// Forget drops the marks of rules that are no longer there, so the list does not grow
// with the admin's experiments.
func (c *Chats) Forget(keep func(rule string) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.shards {
		for tg, ch := range c.shards[i].chats {
			for r := range ch.Sent {
				if !keep(r) {
					delete(ch.Sent, r)
					c.dirty[i] = true
				}
			}
			kept := ch.Due[:0]
			for _, d := range ch.Due {
				if keep(d.Rule) {
					kept = append(kept, d)
				}
			}
			if len(kept) != len(ch.Due) {
				ch.Due = kept
				c.dirty[i] = true
			}
			_ = tg
		}
	}
}

// AddDue puts an automatic message for tg in line. One message of a rule about one
// subscription waits at a time: a newer one replaces it.
func (c *Chats) AddDue(tg int64, d Due) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.chat(tg, true)
	ch.Due = slices.DeleteFunc(ch.Due, func(x Due) bool { return x.Rule == d.Rule && x.User == d.User })
	ch.Due = append(ch.Due, d)
	c.touch(tg)
}

// TakeDue takes the automatic messages whose time has come.
func (c *Chats) TakeDue(now time.Time) []DueItem {
	t := now.Unix()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []DueItem
	for i := range c.shards {
		for tg, ch := range c.shards[i].chats {
			if len(ch.Due) == 0 {
				continue
			}
			kept := ch.Due[:0]
			for _, d := range ch.Due {
				if d.At <= t {
					out = append(out, DueItem{TgID: tg, Due: d})
				} else {
					kept = append(kept, d)
				}
			}
			if len(kept) != len(ch.Due) {
				ch.Due = kept
				c.dirty[i] = true
			}
		}
	}
	slices.SortFunc(out, func(a, b DueItem) int { return int(a.At - b.At) })
	return out
}

// Load reads the list from the panel and merges it with what was seen meanwhile.
func (c *Chats) Load(ctx context.Context) error {
	keys, err := c.kv.Keys(ctx, chatsPrefix)
	if err != nil {
		return err
	}
	type got struct {
		i     int
		ver   int64
		chats map[int64]*Chat
	}
	var all []got
	for _, k := range keys {
		i, ok := shardIndex(k)
		if !ok {
			continue
		}
		b, ver, err := c.kv.Get(ctx, k)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		chats, err := decodeShard(b)
		if err != nil {
			c.log.Warn("chats: a part of the list does not read, it starts over", "key", k, "err", err)
			chats = map[int64]*Chat{}
		}
		all = append(all, got{i, ver, chats})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, g := range all {
		s := &c.shards[g.i]
		s.ver = g.ver
		local := s.chats
		s.chats = g.chats
		for tg, ch := range local {
			s.chats[tg] = merge(s.chats[tg], ch)
			c.dirty[g.i] = true
		}
	}
	c.loaded = true
	return nil
}

func shardIndex(key string) (int, bool) {
	h, ok := strings.CutPrefix(key, chatsPrefix)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseUint(h, 16, 16)
	if err != nil || i >= chatShards {
		return 0, false
	}
	return int(i), true
}

func decodeShard(b []byte) (map[int64]*Chat, error) {
	var raw map[string]*Chat
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make(map[int64]*Chat, len(raw))
	for k, v := range raw {
		tg, err := strconv.ParseInt(k, 10, 64)
		if err != nil || v == nil {
			continue
		}
		out[tg] = v
	}
	return out, nil
}

// merge is stored and local together: the earliest first sight, the latest of the rest.
func merge(stored, local *Chat) *Chat {
	if stored == nil {
		return local
	}
	out := stored.copy()
	if local.First != 0 && (out.First == 0 || local.First < out.First) {
		out.First = local.First
	}
	out.Start = out.Start || local.Start
	if local.Seen > out.Seen {
		out.Seen, out.Blocked = local.Seen, local.Blocked
	}
	if local.Blocked > out.Seen {
		out.Blocked = local.Blocked
	}
	if local.Name != "" {
		out.Name = local.Name
	}
	for k, v := range local.Sent {
		if out.Sent == nil {
			out.Sent = map[string]int64{}
		}
		out.Sent[k] = max(out.Sent[k], v)
	}
	for _, d := range local.Due {
		if !slices.Contains(out.Due, d) {
			out.Due = append(out.Due, d)
		}
	}
	return &out
}

// Run reads the list (again until it reads) and then writes what changed every few
// seconds, until ctx ends; what is left goes then.
func (c *Chats) Run(ctx context.Context) {
	for wait := time.Second; ctx.Err() == nil; wait = min(2*wait, time.Minute) {
		err := c.Load(ctx)
		if err == nil {
			break
		}
		if ctx.Err() == nil {
			c.log.Warn("chats: read the list", "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			c.Flush(fctx)
			cancel()
			return
		case <-t.C:
			c.Flush(ctx)
		}
	}
}

// Flush writes the parts of the list that changed. A part another writer changed is read
// and merged first.
func (c *Chats) Flush(ctx context.Context) {
	c.mu.Lock()
	if !c.loaded {
		c.mu.Unlock()
		return
	}
	type job struct {
		i    int
		ver  int64
		body []byte
	}
	var jobs []job
	for i := range c.dirty {
		s := &c.shards[i]
		raw := make(map[string]*Chat, len(s.chats))
		for tg, ch := range s.chats {
			raw[strconv.FormatInt(tg, 10)] = ch
		}
		b, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		jobs = append(jobs, job{i, s.ver, b})
		delete(c.dirty, i)
	}
	c.mu.Unlock()
	for _, j := range jobs {
		ver := j.ver
		if ver == 0 {
			ver = -1 // none stored: only if none is
		}
		nv, err := c.kv.Put(ctx, shardKey(j.i), j.body, ver, 0)
		c.mu.Lock()
		switch {
		case err == nil:
			c.shards[j.i].ver = nv
			delete(c.full, j.i)
		case errors.Is(err, ErrConflict):
			c.mu.Unlock()
			c.reread(ctx, j.i)
			c.mu.Lock()
			c.dirty[j.i] = true
		default:
			c.dirty[j.i] = true
			if strings.Contains(err.Error(), "HTTP 413") || strings.Contains(err.Error(), "HTTP 400") {
				if !c.full[j.i] {
					c.full[j.i] = true
					c.log.Error("chats: a part of the list is too large for the panel", "key", shardKey(j.i), "bytes", len(j.body))
				}
			} else if ctx.Err() == nil {
				c.log.Warn("chats: write", "key", shardKey(j.i), "err", err)
			}
		}
		c.mu.Unlock()
	}
}

// reread takes the stored part i and merges what is in memory into it.
func (c *Chats) reread(ctx context.Context, i int) {
	b, ver, err := c.kv.Get(ctx, shardKey(i))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return
	}
	stored := map[int64]*Chat{}
	if err == nil {
		if m, err := decodeShard(b); err == nil {
			stored = m
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.shards[i]
	for tg, ch := range s.chats {
		stored[tg] = merge(stored[tg], ch)
	}
	s.chats, s.ver = stored, ver
}

func cut(s string, n int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, s))
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
