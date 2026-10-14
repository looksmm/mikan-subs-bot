package addon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"subsbot/internal/plus"
	"subsbot/internal/plus/plustest"
	"subsbot/internal/tgbot"
)

// The settings go out as one text and come back in whole, or not at all.
func TestExportImport(t *testing.T) {
	fake, srv := plustest.NewKV("p")
	t.Cleanup(srv.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	kv := plus.NewKV(srv.URL+"/panel/v2", "p", nil)
	set := plus.NewStore(kv, log)
	set.Check(tgbot.CheckTexts)
	d := Deps{ID: "subs-bot", Token: "tok", Version: "test", Set: set, Log: log, Texts: tgbot.TextEntries,
		Menu: plus.NewMenu(kv, log), Auto: plus.NewAuto(kv, log, time.Now)}
	h := Handler(d)
	rpcCall := func(method, path string, body any) (int, map[string]any) {
		rec := call(h, "tok", http.MethodPost, "/v2/ui/rpc", map[string]any{"method": method, "path": path, "body": body})
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	menu := plus.Menu{On: true, Main: "main", Screens: []plus.Screen{{ID: "main", Rows: [][]plus.Action{{{Label: "Подписка", Do: "sub"}}}}}}
	if _, err := d.Menu.Save(context.Background(), menu, nil); err != nil {
		t.Fatal(err)
	}
	code, out := rpcCall("GET", "/export", nil)
	if code != http.StatusOK || out["kind"] != bundleKind || out["menu"] == nil || out["settings"] == nil {
		t.Fatalf("export: %d %v", code, out)
	}

	// A bad menu: nothing is written, the settings neither.
	puts := fake.Puts
	bad := map[string]any{"kind": bundleKind, "settings": map[string]any{"look": map[string]any{"on": true}, "texts": map[string]any{}},
		"menu": map[string]any{"on": true, "main": "gone", "screens": []any{}}}
	if code, out := rpcCall("POST", "/import", bad); code != http.StatusUnprocessableEntity || out["field"] != "main" {
		t.Fatalf("bad import: %d %v", code, out)
	}
	if fake.Puts != puts {
		t.Fatalf("a refused import wrote %d values", fake.Puts-puts)
	}
	// Whole: written, and the rules start over.
	good := map[string]any{"kind": bundleKind, "settings": map[string]any{"look": map[string]any{"on": true}, "texts": map[string]any{}},
		"auto": map[string]any{"rules": []any{map[string]any{"id": "r1", "on": true, "trigger": "start", "delay": 60, "created": 1,
			"message": map[string]any{"text": "Привет"}}}}}
	if code, out := rpcCall("POST", "/import", good); code != http.StatusOK {
		t.Fatalf("import: %d %v", code, out)
	}
	if !set.Get().Look.On || d.Auto.Get().Rules[0].Created <= 1 || len(d.Menu.Get().Screens) != 1 {
		t.Fatalf("after import: look %v, created %d, menu %+v", set.Get().Look.On, d.Auto.Get().Rules[0].Created, d.Menu.Get())
	}
	if code, _ := rpcCall("POST", "/import", map[string]any{"kind": "x"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("not ours: %d", code)
	}
}
