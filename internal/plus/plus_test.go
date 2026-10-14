package plus_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"subsbot/internal/plus"
	"subsbot/internal/plus/plustest"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDefaultsAreValid(t *testing.T) {
	d := plus.Default()
	d.Normalize()
	if err := d.Valid(); err != nil {
		t.Fatal(err)
	}
	// Off until the admin turns it on (custom emoji need Premium), the set ready in it.
	if d.Look.On || d.Look.Icons["sub"].ID == "" || d.Look.Icons["back"].ID == "" {
		t.Fatalf("defaults: %+v", d.Look)
	}
}

func TestSettingsRefuseWhatTelegramWouldNot(t *testing.T) {
	for _, c := range []struct {
		name  string
		edit  func(s *plus.Settings)
		field string
	}{
		{"an id with letters", func(s *plus.Settings) { s.Look.Icons["sub"] = plus.Icon{ID: "abc"} }, "look.icons.sub"},
		{"an unknown colour", func(s *plus.Settings) { s.Look.Icons["sub"] = plus.Icon{Style: "purple"} }, "look.icons.sub"},
		{"a letter as an emoji", func(s *plus.Settings) { s.Look.Emoji["a"] = "5134197574856607298" }, "look.emoji"},
		{"a word as an emoji", func(s *plus.Settings) { s.Look.Emoji["Привет"] = "5134197574856607298" }, "look.emoji"},
		{"an emoji with a bad id", func(s *plus.Settings) { s.Look.Emoji["📅"] = "x1" }, "look.emoji"},
		{"a bad test chat", func(s *plus.Settings) { s.TestChat = -5 }, "test_chat"},
	} {
		s := plus.Default()
		c.edit(&s)
		s.Normalize()
		var fe *plus.FieldError
		if err := s.Valid(); !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("%s: got %v, want a field error on %s", c.name, err, c.field)
		}
	}
	// The admin's own menu buttons and an icon without an id (only a colour) are fine.
	s := plus.Default()
	s.Look.Icons["btn:c3"] = plus.Icon{ID: "5134263008683362081", Style: "danger"}
	s.Look.Icons["page:c4"] = plus.Icon{Style: "success"}
	s.Look.Emoji["⛔️"] = "5134520960124192408"
	s.Normalize()
	if err := s.Valid(); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeDropsEmptyRows(t *testing.T) {
	s := plus.Default()
	// Buttons the bot no longer has, saved before they went.
	s.Look.Icons["partner"] = plus.Icon{ID: "5134245055720067085"}
	s.Look.Icons["nope:"] = plus.Icon{Style: "primary"}
	s.Look.Icons["sub"] = plus.Icon{ID: "  "}
	s.Look.Emoji[" "] = "5134197574856607298"
	s.Look.Emoji["📦"] = " "
	s.Look.Emoji[" 📱 "] = " 5134231118551189370 "
	s.Normalize()
	if _, ok := s.Look.Icons["sub"]; ok {
		t.Fatal("an icon with no id and no colour stayed")
	}
	if _, ok := s.Look.Icons["partner"]; ok {
		t.Fatal("an icon of a button that is gone stayed")
	}
	if err := s.Valid(); err != nil {
		t.Fatalf("settings with a gone button must save: %v", err)
	}
	if _, ok := s.Look.Emoji["📦"]; ok {
		t.Fatal("an emoji with no id stayed")
	}
	if s.Look.Emoji["📱"] != "5134231118551189370" {
		t.Fatalf("emoji not trimmed: %q", s.Look.Emoji)
	}
}

func TestStoreDefaultsUntilSavedAndSaveIsRead(t *testing.T) {
	ctx := context.Background()
	fake, srv := plustest.NewKV("tok")
	defer srv.Close()
	st := plus.NewStore(plus.NewKV(srv.URL+"/panel/v2", "tok", nil), quiet())
	if err := st.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if !st.Loaded() || st.Get().Look.Icons["sub"].ID == "" {
		t.Fatal("nothing saved yet should be the defaults")
	}
	next := *st.Get()
	next.Look.Icons = map[string]plus.Icon{"sub": {ID: "123456", Style: "danger"}}
	if _, err := st.Save(ctx, next); err != nil {
		t.Fatal(err)
	}
	var stored plus.Settings
	if err := json.Unmarshal(fake.Value("settings"), &stored); err != nil {
		t.Fatal(err)
	}
	// Removed icons stay removed: the defaults do not come back over a save.
	if len(stored.Look.Icons) != 1 || stored.Saved == 0 {
		t.Fatalf("stored %+v", stored)
	}
	other := plus.NewStore(plus.NewKV(srv.URL+"/panel/v2", "tok", nil), quiet())
	if err := other.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := other.Get().Look.Icons; len(got) != 1 || got["sub"].Style != "danger" {
		t.Fatalf("another run read %+v", other.Get())
	}
	// A bad save changes nothing.
	bad := next
	bad.Look.Icons = map[string]plus.Icon{"sub": {ID: "x"}}
	if _, err := st.Save(ctx, bad); err == nil {
		t.Fatal("a bad save went through")
	}
	if st.Get().Look.Icons["sub"].ID != "123456" {
		t.Fatal("a bad save changed the settings in memory")
	}
}

func TestKVUpdateRetriesOverAnotherWriter(t *testing.T) {
	ctx := context.Background()
	_, srv := plustest.NewKV("tok")
	defer srv.Close()
	kv := plus.NewKV(srv.URL+"/panel/v2", "tok", nil)
	type counter struct{ N int }
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := plus.Update(ctx, kv, "c", func(c *counter) bool { c.N++; return true }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	b, _, err := kv.Get(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	var c counter
	_ = json.Unmarshal(b, &c)
	// A write lost over another would leave the count short.
	if c.N != 5 {
		t.Fatalf("count %d", c.N)
	}
	if _, err := kv.Put(ctx, "c", []byte("{}"), -1, 0); !errors.Is(err, plus.ErrConflict) {
		t.Fatalf("if absent over a value: %v", err)
	}
	if _, _, err := kv.Get(ctx, "nothing"); !errors.Is(err, plus.ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
	keys, err := kv.Keys(ctx, "")
	if err != nil || strings.Join(keys, ",") != "c" {
		t.Fatalf("keys %v %v", keys, err)
	}
	bad := plus.NewKV(srv.URL+"/panel/v2", "wrong", nil)
	if _, _, err := bad.Get(ctx, "c"); err == nil || errors.Is(err, plus.ErrNotFound) {
		t.Fatalf("a wrong token: %v", err)
	}
}
