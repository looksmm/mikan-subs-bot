package tgate

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// outboxLog runs an outbox and records the jobs in the order they went.
type outboxLog struct {
	mu   sync.Mutex
	done []string
	o    *Outbox
}

func newOutboxLog(lim Limits) *outboxLog {
	return &outboxLog{o: NewOutbox(nil, lim, time.Now, func(int64) {}, func(error) {})}
}

func (l *outboxLog) job(name string, then func()) func(context.Context, *Client) error {
	return func(context.Context, *Client) error {
		l.mu.Lock()
		l.done = append(l.done, name)
		l.mu.Unlock()
		if then != nil {
			then()
		}
		return nil
	}
}

func (l *outboxLog) order() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.done...)
}

// A wave of /start: every chat's removal of its message comes before its menu, as the bot
// queues them. All the menus go before any removal.
func TestOutboxWaveMenusBeforeTidy(t *testing.T) {
	l := newOutboxLog(Limits{Total: 2000, Bulk: 1500, PerChat: 1, Burst: 3, Workers: 1, MaxTries: 5})
	const chats = 300
	all := make(chan struct{})
	var left sync.WaitGroup
	left.Add(2 * chats)
	for i := range int64(chats) {
		l.o.Tidy(i, l.job("tidy", left.Done))
		l.o.Reply(i, fmt.Sprint("menu:", i), 1, l.job("menu", left.Done))
	}
	if n := l.o.BulkPending(); n != 0 {
		t.Fatalf("BulkPending counts the removals: %d", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.o.Run(ctx)
	go func() { left.Wait(); close(all) }()
	select {
	case <-all:
	case <-time.After(10 * time.Second):
		t.Fatalf("not all sent: %d of %d", len(l.order()), 2*chats)
	}
	got := l.order()
	for i, name := range got {
		if want := map[bool]string{true: "menu", false: "tidy"}[i < chats]; name != want {
			t.Fatalf("job %d is a %s, want a %s", i, name, want)
		}
	}
}

// A reply that costs 2 at the front is not passed by cheaper ones behind it while the
// tokens come one by one (it waited until all of them had gone).
func TestOutboxDearReplyKeepsItsTurn(t *testing.T) {
	l := newOutboxLog(Limits{Total: 10, Bulk: 5, PerChat: 1, Burst: 3, Workers: 1, MaxTries: 5})
	dear := make(chan struct{})
	for i := range int64(10) { // the whole bucket
		l.o.Reply(i, "", 1, l.job("cheap", nil))
	}
	l.o.Reply(100, "", 2, l.job("dear", func() { close(dear) }))
	for i := range int64(50) {
		l.o.Reply(200+i, "", 1, l.job("cheap", nil))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.o.Run(ctx)
	select {
	case <-dear:
	case <-time.After(3 * time.Second):
		t.Fatalf("the dear reply did not go: %d sent before", len(l.order()))
	}
	for i, name := range l.order() {
		if name == "dear" && i != 10 {
			t.Fatalf("the dear reply went %dth, want 11th", i+1)
		}
	}
}

// A tap goes before the replies to messages queued ahead of it.
func TestOutboxPressFirst(t *testing.T) {
	l := newOutboxLog(Limits{Total: 2000, Bulk: 1500, PerChat: 1, Burst: 3, Workers: 1, MaxTries: 5})
	first := make(chan struct{})
	var once sync.Once
	for i := range int64(50) {
		l.o.Reply(i, "", 1, l.job("reply", func() { once.Do(func() { close(first) }) }))
	}
	l.o.Press(999, "edit:999:1", l.job("press", func() { once.Do(func() { close(first) }) }))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.o.Run(ctx)
	select {
	case <-first:
	case <-time.After(3 * time.Second):
		t.Fatal("nothing sent")
	}
	if got := l.order(); got[0] != "press" {
		t.Fatalf("first sent: %s, want the tap", got[0])
	}
}
