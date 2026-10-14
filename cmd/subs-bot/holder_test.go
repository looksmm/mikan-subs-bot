package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePanel is the panel's API for addons as a bot addon sees it: the Bot API proxy, the
// events and the bot's RPC; holder says whether this addon holds the bot.
type fakePanel struct {
	mu     sync.Mutex
	holder bool
	events []map[string]any // the feed, handed out once
	acked  []int64
	sent   []string // Bot API methods the addon called that reached Telegram
	ups    []map[string]any
}

func (f *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(code int, v any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(v) }
	switch {
	case strings.HasPrefix(r.URL.Path, "/bot0:tok/"):
		method := strings.TrimPrefix(r.URL.Path, "/bot0:tok/")
		if !f.holder {
			write(401, map[string]any{"ok": false, "error_code": 401, "description": "Unauthorized"})
			return
		}
		switch method {
		case "getUpdates":
			ups := f.ups
			f.ups = nil
			if ups == nil {
				ups = []map[string]any{}
			}
			write(200, map[string]any{"ok": true, "result": ups})
		case "getMe":
			write(200, map[string]any{"ok": true, "result": map[string]any{"id": 1, "is_bot": true, "first_name": "Example", "username": "example_bot"}})
		default:
			f.sent = append(f.sent, method)
			write(200, map[string]any{"ok": true, "result": map[string]any{"message_id": 5, "chat": map[string]any{"id": 1, "type": "private"}}})
		}
	case r.URL.Path == "/panel/v2/events":
		evs := f.events
		f.events = nil
		if evs == nil {
			evs = []map[string]any{}
		}
		write(200, map[string]any{"events": evs})
	case r.URL.Path == "/panel/v2/events/ack":
		var in struct {
			ID int64 `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.acked = append(f.acked, in.ID)
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(r.URL.Path, "/panel/v2/bot/"):
		if !f.holder {
			write(403, map[string]any{"error": map[string]string{"code": "forbidden", "message": "the addon does not hold the bot"}})
			return
		}
		write(404, map[string]any{"error": map[string]string{"code": "not_found", "message": "not in this fake"}})
	case strings.HasPrefix(r.URL.Path, "/panel/v2/kv/"):
		write(404, map[string]any{"error": map[string]string{"code": "not_found"}})
	default:
		write(404, map[string]any{"error": map[string]string{"code": "not_found"}})
	}
}

func (f *fakePanel) snapshot() (acked []int64, sent []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.acked...), append([]string(nil), f.sent...)
}

// An addon the bot is not given to passes the notices by (the holder tells them) instead of
// sending them all late once it gets the bot; given the bot, it sets the chats up again.
func TestNotHolderPassesNoticesAndTakesOverLater(t *testing.T) {
	f := &fakePanel{events: []map[string]any{
		{"id": 11, "type": "user.expiring", "user_id": 3, "tg_id": 555, "data": map[string]any{"days": 3}},
		{"id": 12, "type": "traffic.90", "user_id": 3, "tg_id": 555, "data": map[string]any{}},
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := newAddon(srv.URL+"/panel/v2", "tok", srv.URL, "0:tok", log)
	a.updatesWait = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	until := func(what string, ok func(acked []int64, sent []string) bool) {
		t.Helper()
		for start := time.Now(); time.Since(start) < 10*time.Second; time.Sleep(20 * time.Millisecond) {
			if ok(f.snapshot()) {
				return
			}
		}
		acked, sent := f.snapshot()
		t.Fatalf("%s: acked %v, sent %v", what, acked, sent)
	}
	until("the notices passed by", func(acked []int64, _ []string) bool { return len(acked) == 2 && acked[1] == 12 })
	if _, sent := f.snapshot(); len(sent) != 0 {
		t.Fatalf("sent while the bot is another addon's: %v", sent)
	}
	f.mu.Lock()
	f.holder = true
	f.mu.Unlock()
	until("the chats set up on taking the bot", func(_ []int64, sent []string) bool {
		return strings.Contains(strings.Join(sent, ","), "setMyCommands")
	})
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the addon did not stop")
	}
}
