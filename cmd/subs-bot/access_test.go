package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeState(t *testing.T, path, body string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func TestAccessOfState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	writeState(t, path, `{"adapters":{"subs-bot":{"version":"custom","image":"","listen":"","token":"x","protocol":2,
		"source":"external","kind":"bot","mode":"client","panel_token":"tok","status":"running"},
		"cryptobot":{"token":"y","status":"running"}},"panel_api":"127.0.0.1:43338"}`)
	a, err := accessOfState(path, "subs-bot")
	if err != nil {
		t.Fatal(err)
	}
	want := access{panelURL: "http://127.0.0.1:43338/panel/v2", panelToken: "tok", botURL: "http://127.0.0.1:43338", botToken: "0:tok"}
	if a != want {
		t.Fatalf("got %+v, want %+v", a, want)
	}
	for _, c := range []struct{ name, body, id, err string }{
		{"not installed", `{"adapters":{},"panel_api":"127.0.0.1:1"}`, "subs-bot", "not installed"},
		{"stopped", `{"adapters":{"subs-bot":{"panel_token":"t","status":"failed"}},"panel_api":"127.0.0.1:1"}`, "subs-bot", "not running"},
		{"a payment adapter", `{"adapters":{"cryptobot":{"status":"running"}},"panel_api":"127.0.0.1:1"}`, "cryptobot", "no panel token"},
		{"no API", `{"adapters":{"subs-bot":{"panel_token":"t","status":"running"}}}`, "subs-bot", "no address"},
		{"broken", `{`, "subs-bot", "unexpected end"},
	} {
		writeState(t, path, c.body)
		if _, err := accessOfState(path, c.id); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.err)
		}
	}
}

// The bot starts with the state's tokens, starts again with new ones after a reinstall,
// and stops while the addon is gone.
func TestWatchState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := func(tok string) string {
		return `{"adapters":{"subs-bot":{"panel_token":"` + tok + `","status":"running"}},"panel_api":"127.0.0.1:43338"}`
	}
	var (
		mu      sync.Mutex
		running []string // tokens of the runs going on
		started []string
	)
	run := func(ctx context.Context, a access) {
		mu.Lock()
		running = append(running, a.panelToken)
		started = append(started, a.panelToken)
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		for i, tok := range running {
			if tok == a.panelToken {
				running = append(running[:i], running[i+1:]...)
				break
			}
		}
		mu.Unlock()
	}
	until := func(what string, ok func() bool) {
		t.Helper()
		for start := time.Now(); time.Since(start) < 5*time.Second; time.Sleep(5 * time.Millisecond) {
			mu.Lock()
			done := ok()
			mu.Unlock()
			if done {
				return
			}
		}
		t.Fatalf("%s: running %q, started %q", what, running, started)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	writeState(t, path, `{"adapters":{}}`)
	go func() {
		watchState(ctx, path, "subs-bot", 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)), run)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if len(started) != 0 {
		t.Fatalf("started without an addon: %q", started)
	}
	writeState(t, path, state("one"))
	until("the first run", func() bool { return len(running) == 1 && running[0] == "one" })
	writeState(t, path, state("two"))
	until("the run with the new token", func() bool { return len(running) == 1 && running[0] == "two" })
	writeState(t, path, `{"adapters":{}}`)
	until("stopped once removed", func() bool { return len(running) == 0 })
	writeState(t, path, state("two"))
	until("back after a reinstall", func() bool { return len(running) == 1 })
	cancel()
	<-done
	if len(running) != 0 {
		t.Fatalf("still running after the end: %q", running)
	}
	if got := strings.Join(started, ","); got != "one,two,two" {
		t.Fatalf("started %s", got)
	}
}
