package tgate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeTelegram turns down any body with an icon as Telegram would a custom emoji the bot may
// not use, or says the message is not modified when told to.
type fakeTelegram struct {
	mu          sync.Mutex
	bodies      []string
	notModified bool
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(b))
	nm := f.notModified
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case nm:
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: message is not modified"})
	case strings.Contains(string(b), "icon_custom_emoji_id"):
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: DOCUMENT_INVALID"})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 7, "chat": map[string]any{"id": 1, "type": "private"}}})
	}
}

func TestDressedBodyRefusedGoesPlain(t *testing.T) {
	f := &fakeTelegram{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL, "0:t", nil)
	var refused []string
	c.Decorate(func(method string, body []byte) ([]byte, bool) {
		return []byte(strings.Replace(string(body), `"text":"Подписка"`, `"text":"Подписка","icon_custom_emoji_id":"1"`, 1)), true
	}, func(method string, err error) { refused = append(refused, method+": "+err.Error()) })
	kb := &Keyboard{InlineKeyboard: [][]Button{{{Text: "Подписка", CallbackData: "s"}}}}
	m, err := c.Send(context.Background(), 1, "привет", kb, false)
	if err != nil || m.MessageID != 7 {
		t.Fatalf("send: %v %+v", err, m)
	}
	if len(f.bodies) != 2 || !strings.Contains(f.bodies[0], "icon_custom_emoji_id") || strings.Contains(f.bodies[1], "icon_custom_emoji_id") {
		t.Fatalf("bodies %q", f.bodies)
	}
	if len(refused) != 1 || !strings.Contains(refused[0], "DOCUMENT_INVALID") {
		t.Fatalf("refused %q", refused)
	}
	// An edit to the same content is no refusal of the look: it is not sent again plain,
	// which would take the icons off a menu that has them.
	f.mu.Lock()
	f.bodies, f.notModified = nil, true
	f.mu.Unlock()
	if err := c.Edit(context.Background(), 1, 7, "привет", kb); err != nil {
		t.Fatalf("not modified is no error: %v", err)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("an unchanged edit went %d times", len(f.bodies))
	}
}

func TestUndressedClientIsMikans(t *testing.T) {
	f := &fakeTelegram{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL, "0:t", nil)
	if _, err := c.Send(context.Background(), 1, "x", nil, false); err != nil {
		t.Fatal(err)
	}
	c.Decorate(func(string, []byte) ([]byte, bool) { return nil, false }, nil)
	if _, err := c.Send(context.Background(), 1, "x", nil, false); err != nil {
		t.Fatal(err)
	}
	if len(f.bodies) != 2 || f.bodies[0] != f.bodies[1] {
		t.Fatalf("bodies %q", f.bodies)
	}
}
