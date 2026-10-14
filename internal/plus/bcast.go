package plus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// A broadcast: one message to a part of the bot's accounts, sent in the background at the
// bulk pace. The job and its list of recipients live in the panel's store, so a restart
// goes on where it stopped.

const (
	bcastKey        = "broadcast"
	bcastToPrefix   = "broadcast/to/"
	bcastHistoryKey = "broadcast/history"
	// BcastChunk is how many recipients one value holds: ~50 KB of ids.
	BcastChunk = 4000
	historyLen = 10
)

// Broadcast states.
const (
	BcastScheduled = "scheduled"
	BcastRunning   = "running"
	BcastDone      = "done"
	BcastCancelled = "cancelled"
)

// Broadcast is a job.
type Broadcast struct {
	ID       string  `json:"id"`
	Audience string  `json:"audience"`
	Message  Message `json:"message"`
	// At: when to start (unix); 0 at once.
	At      int64  `json:"at,omitempty"`
	State   string `json:"state"`
	Total   int    `json:"total"`
	Sent    int    `json:"sent"`
	Failed  int    `json:"failed"`
	Blocked int    `json:"blocked"`
	// Next is the index of the next recipient to send to.
	Next     int    `json:"next"`
	Chunks   int    `json:"chunks"`
	Created  int64  `json:"created"`
	Started  int64  `json:"started,omitempty"`
	Finished int64  `json:"finished,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Active: still to send, now or at its time.
func (b *Broadcast) Active() bool {
	return b != nil && (b.State == BcastScheduled || b.State == BcastRunning)
}

// Audience is a part of the accounts a broadcast goes to.
type Audience struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Hint string `json:"hint"`
	// Users: telling it needs the panel's subscribers (permission users:all).
	Users bool `json:"users,omitempty"`
}

// Audiences, in the order the page lists them.
var Audiences = []Audience{
	{Key: "all", Name: "Все", Hint: "все, кто запускал бота, и все подписчики панели"},
	{Key: "active", Name: "С действующей подпиской", Hint: "подписка работает сейчас", Users: true},
	{Key: "expired", Name: "Подписка закончилась", Hint: "была, но сейчас не действует", Users: true},
	{Key: "none", Name: "Без подписки", Hint: "запускали бота, но ни разу не оформляли", Users: true},
}

// AudienceOf is the audience of key.
func AudienceOf(key string) (Audience, bool) {
	i := slices.IndexFunc(Audiences, func(a Audience) bool { return a.Key == key })
	if i < 0 {
		return Audience{}, false
	}
	return Audiences[i], true
}

// Valid checks a broadcast the page starts.
func (b *Broadcast) Valid() error {
	b.Message.Normalize()
	if _, ok := AudienceOf(b.Audience); !ok {
		return &FieldError{"audience", "выберите, кому отправить"}
	}
	return b.Message.Valid("message", "рассылка")
}

// Broadcasts keeps the job in the panel's store.
type Broadcasts struct{ kv *KV }

func NewBroadcasts(kv *KV) *Broadcasts { return &Broadcasts{kv: kv} }

// Job is the current or the last broadcast; nil when there never was one.
func (s *Broadcasts) Job(ctx context.Context) (*Broadcast, error) {
	b, _, err := s.kv.Get(ctx, bcastKey)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j Broadcast
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// Save writes the job.
func (s *Broadcasts) Save(ctx context.Context, j *Broadcast) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = s.kv.Put(ctx, bcastKey, b, 0, 0)
	return err
}

// SetRecipients writes the job's recipients, in chunks.
func (s *Broadcasts) SetRecipients(ctx context.Context, j *Broadcast, to []int64) error {
	j.Total, j.Chunks = len(to), 0
	for i := 0; i < len(to); i += BcastChunk {
		part := to[i:min(i+BcastChunk, len(to))]
		b, _ := json.Marshal(part)
		if _, err := s.kv.Put(ctx, chunkKey(j.ID, j.Chunks), b, 0, 0); err != nil {
			return err
		}
		j.Chunks++
	}
	return nil
}

func chunkKey(id string, n int) string { return bcastToPrefix + id + "/" + strconv.Itoa(n) }

// Recipients reads the job's chunk n.
func (s *Broadcasts) Recipients(ctx context.Context, j *Broadcast, n int) ([]int64, error) {
	b, _, err := s.kv.Get(ctx, chunkKey(j.ID, n))
	if err != nil {
		return nil, err
	}
	var out []int64
	return out, json.Unmarshal(b, &out)
}

// Done ends the job: its recipients go, it joins the history.
func (s *Broadcasts) Done(ctx context.Context, j *Broadcast) error {
	if err := s.Save(ctx, j); err != nil {
		return err
	}
	keys, err := s.kv.Keys(ctx, bcastToPrefix+j.ID+"/")
	if err == nil {
		for _, k := range keys {
			_ = s.kv.Delete(ctx, k)
		}
	}
	_, err = Update(ctx, s.kv, bcastHistoryKey, func(h *[]Broadcast) bool {
		short := *j
		short.Message.Text = cut(short.Message.Text, 200)
		short.Message.Rows = nil
		*h = slices.DeleteFunc(*h, func(x Broadcast) bool { return x.ID == j.ID })
		*h = append([]Broadcast{short}, *h...)
		if len(*h) > historyLen {
			*h = (*h)[:historyLen]
		}
		return true
	})
	return err
}

// History is the last broadcasts, the newest first.
func (s *Broadcasts) History(ctx context.Context) ([]Broadcast, error) {
	b, _, err := s.kv.Get(ctx, bcastHistoryKey)
	if errors.Is(err, ErrNotFound) {
		return []Broadcast{}, nil
	}
	if err != nil {
		return nil, err
	}
	var h []Broadcast
	return h, json.Unmarshal(b, &h)
}

// Leftovers removes the recipients of jobs that are no longer the current one (a job
// replaced while it ran).
func (s *Broadcasts) Leftovers(ctx context.Context, keep string) {
	keys, err := s.kv.Keys(ctx, bcastToPrefix)
	if err != nil {
		return
	}
	for _, k := range keys {
		id, _, _ := strings.Cut(strings.TrimPrefix(k, bcastToPrefix), "/")
		if id != keep {
			_ = s.kv.Delete(ctx, k)
		}
	}
}

// NewID is a job's id from its time.
func NewID(unix int64) string { return fmt.Sprintf("b%x", unix) }
