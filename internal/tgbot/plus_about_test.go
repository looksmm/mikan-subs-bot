package tgbot

import (
	"context"
	"strings"
	"testing"

	"subsbot/internal/plus"
	"subsbot/internal/tgate"
)

// exampleAbout is «О сервисе» as ExampleVPN's: the privacy policy and the agreement, short.
func exampleAbout(s *plus.Settings) {
	s.About = plus.About{On: true, WithSupport: true, Items: []plus.AboutItem{
		{ID: "cmv58jrv6", Label: "Конфиденциальность"}, {ID: "cmv58ga30", Label: "Соглашение"},
	}}
}

// supportStub knows the support link besides what panelStub does.
type supportStub struct{ panelStub }

func (supportStub) SupportURL(context.Context) string { return "https://t.me/plus_support" }

func labels(cfg Config) []string {
	var out []string
	for _, mb := range cfg.Buttons {
		mark := ""
		if mb.Row {
			mark = "+"
		}
		out = append(out, mark+mb.ID)
	}
	return out
}

func TestMenuTakesTheDocumentsIntoAbout(t *testing.T) {
	b := lookBot(t, exampleAbout)
	// ExampleVPN's menu: sub, devices in its row, connect (off), renew, support, the two
	// documents, the page, the old link and the Mini App.
	cfg := exampleMenu()
	got := strings.Join(labels(b.menuConfig(cfg)), " ")
	want := "sub +devices connect renew about +support app c9 c10"
	if got != want {
		t.Fatalf("menu %s, want %s", got, want)
	}
	// Off: mikan's menu as it is.
	if got := labels((&Bot{}).menuConfig(cfg)); len(got) != len(cfg.Buttons) {
		t.Fatalf("mikan's menu changed: %v", got)
	}
	// Not with support: where the documents were.
	b2 := lookBot(t, func(s *plus.Settings) { exampleAbout(s); s.About.WithSupport = false })
	if got := strings.Join(labels(b2.menuConfig(cfg)), " "); got != "sub +devices connect renew +support app about c9 c10" {
		t.Fatalf("in place: %s", got)
	}
}

func TestAboutScreen(t *testing.T) {
	b := lookBot(t, exampleAbout)
	b.d.Backend = brandOnly{}
	w := wordsFor("ru")
	text, kb, ok := b.aboutScreen(context.Background(), exampleMenu(), w, "")
	if !ok || !strings.Contains(text, "<b>О сервисе</b>") || !strings.Contains(text, "ExampleVPN — быстрый VPN") {
		t.Fatalf("text %q", text)
	}
	rows := kb.InlineKeyboard
	if len(rows) != 2 || len(rows[0]) != 2 || rows[0][0].Text != "Конфиденциальность" || rows[0][1].Text != "Соглашение" ||
		rows[0][0].URL != "https://example.com/privacy" || rows[1][0].CallbackData != "m" {
		t.Fatalf("buttons %+v", rows)
	}
	// Its own text, Markdown and {brand}.
	own := lookBot(t, func(s *plus.Settings) { exampleAbout(s); s.About.Text = "_{brand}_ — это мы" })
	own.d.Backend = brandOnly{}
	if text, _, _ := own.aboutScreen(context.Background(), exampleMenu(), w, ""); text != "<i>ExampleVPN</i> — это мы" {
		t.Fatalf("own text %q", text)
	}
	if _, _, ok := (&Bot{}).aboutScreen(context.Background(), exampleMenu(), w, ""); ok {
		t.Fatal("mikan's bot has no «О сервисе»")
	}
}

func TestWelcomeHasAboutBeforeSupport(t *testing.T) {
	b := lookBot(t, exampleAbout)
	b.d.Backend = supportStub{}
	// mikan's welcome rows: trial, buy, support.
	rows := [][]tgate.Button{{{Text: "🎁 Попробовать", CallbackData: "tr"}}, {{Text: "🛒 Купить", CallbackData: "b"}}, {{Text: "💬 Поддержка", URL: "https://t.me/plus_support"}}}
	got := b.welcomeRows(context.Background(), exampleMenu(), rows)
	var flat []string
	for _, r := range got {
		var row []string
		for _, x := range r {
			row = append(row, x.Text)
		}
		flat = append(flat, strings.Join(row, " | "))
	}
	want := []string{"🎁 Попробовать", "🛒 Купить", "ℹ️ О сервисе | 💬 Поддержка", "📄 О сервисе"}
	if strings.Join(flat, "\n") != strings.Join(want, "\n") {
		t.Fatalf("welcome:\n%s\nwant:\n%s", strings.Join(flat, "\n"), strings.Join(want, "\n"))
	}
	// A page inside «О сервисе» opens without a subscription even when kept off the welcome.
	pageIn := lookBot(t, func(s *plus.Settings) {
		exampleAbout(s)
		s.About.Items = append(s.About.Items, plus.AboutItem{ID: "c9"})
		s.Welcome.Hide = []string{"c9"}
	})
	pageIn.d.Backend = brandOnly{}
	if _, _, ok := pageIn.welcomePage(context.Background(), exampleMenu(), wordsFor("ru"), "p", "c9", ""); !ok {
		t.Fatal("a page of «О сервисе» did not open")
	}
}
