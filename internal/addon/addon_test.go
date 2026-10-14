package addon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"subsbot/internal/plus"
	"subsbot/internal/plus/plustest"
	"subsbot/internal/tgbot"
)

func setup(t *testing.T) (http.Handler, *plustest.KV, string) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	_ = os.WriteFile(state, []byte(`{"adapters":{"subs-bot":{"token":"panel-calls-with","panel_token":"p","source":"external","status":"running"},
		"other":{"token":"other-token","source":"external","status":"running"}},"panel_api":"127.0.0.1:1"}`), 0o600)
	fake, srv := plustest.NewKV("p")
	t.Cleanup(srv.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	set := plus.NewStore(plus.NewKV(srv.URL+"/panel/v2", "p", nil), log)
	set.Check(tgbot.CheckTexts)
	h := Handler(Deps{ID: "subs-bot", State: state, Version: "test", Set: set, Log: log, Texts: tgbot.TextEntries,
		MenuButtons: func(context.Context) []plus.Button {
			return []plus.Button{{Key: "btn:c7", Name: "📰 Новости"}}
		},
		Refused: func() (string, time.Time) { return "telegram 400: DOCUMENT_INVALID", time.Unix(1700000000, 0) }})
	return h, fake, state
}

func call(h http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestOnlyThePanelGetsIn(t *testing.T) {
	h, _, _ := setup(t)
	for _, tok := range []string{"", "wrong", "other-token", "p"} {
		if rec := call(h, tok, http.MethodGet, "/v2/info", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", tok, rec.Code)
		}
	}
	rec := call(h, "panel-calls-with", http.MethodGet, "/v2/info", nil)
	var info struct {
		ID       string `json:"id"`
		Protocol int    `json:"protocol"`
		UI       struct {
			Entry string `json:"entry"`
		} `json:"ui"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &info) != nil || info.ID != "subs-bot" || info.Protocol != 2 || info.UI.Entry != "/ui/index.html" {
		t.Fatalf("info %d %s", rec.Code, rec.Body)
	}
	for _, f := range []string{"/ui/index.html", "/ui/bot.js", "/ui/common.js", "/ui/style.css", "/ui/bot.css"} {
		rec := call(h, "panel-calls-with", http.MethodGet, f, nil)
		if rec.Code != http.StatusOK || rec.Header().Get("ETag") == "" {
			t.Errorf("%s: %d", f, rec.Code)
		}
	}
	// Only the files of ui/ are served: a path out of it is cleaned away (a redirect) or not found.
	for _, p := range []string{"/ui/../addon.go", "/ui/%2e%2e/addon.go", "/ui/nothing.js"} {
		if rec := call(h, "panel-calls-with", http.MethodGet, p, nil); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "package addon") {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

func TestPageStatusAndSave(t *testing.T) {
	h, fake, _ := setup(t)
	tok := "panel-calls-with"
	rec := call(h, tok, http.MethodPost, "/v2/ui/rpc", map[string]any{"method": "GET", "path": "/status"})
	var v statusView
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	if v.Buttons[len(v.Buttons)-1].Key != "btn:c7" || v.Refused == nil || v.Settings.Look.Icons["sub"].ID == "" {
		t.Fatalf("status %+v", v)
	}
	next := *v.Settings
	next.Look.Icons = map[string]plus.Icon{"sub": {ID: "123456", Style: "success"}, "btn:c7": {Style: "danger"}}
	rec = call(h, tok, http.MethodPost, "/v2/ui/rpc", map[string]any{"method": "PUT", "path": "/settings", "body": next})
	if rec.Code != http.StatusOK {
		t.Fatalf("save %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(string(fake.Value("settings")), `"btn:c7"`) {
		t.Fatalf("stored %s", fake.Value("settings"))
	}
	bad := next
	bad.Look.Icons = map[string]plus.Icon{"sub": {ID: "not a number"}}
	rec = call(h, tok, http.MethodPost, "/v2/ui/rpc", map[string]any{"method": "PUT", "path": "/settings", "body": bad})
	var e apiError
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != http.StatusUnprocessableEntity || e.Field != "look.icons.sub" || e.Message == "" {
		t.Fatalf("bad save %d %s", rec.Code, rec.Body)
	}
	if len(v.Texts) < 100 || v.Texts[0].Key != "sub.screen" {
		t.Fatalf("texts: %d", len(v.Texts))
	}
	badText := next
	badText.Texts = map[string]string{"subTitle": "Подписка без имени"}
	rec = call(h, tok, http.MethodPost, "/v2/ui/rpc", map[string]any{"method": "PUT", "path": "/settings", "body": badText})
	e = apiError{}
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != http.StatusUnprocessableEntity || e.Field != "texts.subTitle" || !strings.Contains(e.Message, "%s") {
		t.Fatalf("a text without its %%s: %d %s", rec.Code, rec.Body)
	}
	goodText := next
	goodText.Texts = map[string]string{"back": "◀️ В меню", "sub.screen": "**{name}**"}
	if rec := call(h, tok, http.MethodPost, "/v2/ui/rpc", map[string]any{"method": "PUT", "path": "/settings", "body": goodText}); rec.Code != http.StatusOK {
		t.Fatalf("texts save %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(string(fake.Value("settings")), `"back":"◀️ В меню"`) {
		t.Fatalf("stored %s", fake.Value("settings"))
	}
	if rec := call(h, tok, http.MethodPost, "/v2/ui/rpc", map[string]any{"method": "DELETE", "path": "/everything"}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown call %d", rec.Code)
	}
}

// The admin reinstalls the addon: the host writes a new token and the old one stops.
func TestNewTokenAfterReinstall(t *testing.T) {
	h, _, state := setup(t)
	if rec := call(h, "panel-calls-with", http.MethodGet, "/v2/health", nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	time.Sleep(10 * time.Millisecond)
	_ = os.WriteFile(state, []byte(`{"adapters":{"subs-bot":{"token":"new-token","source":"external","status":"running"}}}`), 0o600)
	now := time.Now().Add(time.Second)
	_ = os.Chtimes(state, now, now)
	time.Sleep(250 * time.Millisecond)
	if rec := call(h, "new-token", http.MethodGet, "/v2/health", nil); rec.Code != http.StatusOK {
		t.Fatalf("new token: %d", rec.Code)
	}
	time.Sleep(250 * time.Millisecond)
	if rec := call(h, "panel-calls-with", http.MethodGet, "/v2/health", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token: %d", rec.Code)
	}
}
