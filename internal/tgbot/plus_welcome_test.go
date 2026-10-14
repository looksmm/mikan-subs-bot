package tgbot

import (
	"context"
	"strings"
	"testing"

	"subsbot/internal/plus"
)

// ExampleVPN's menu as the panel has it: the agreement and the privacy policy are link
// buttons, the second in the first's row; a page and a button that is off.
func exampleMenu() Config {
	cfg := Default("ru")
	cfg.Buttons = append(cfg.Buttons,
		MenuButton{ID: "cmv58ga30", Action: "url", Label: "📃 Пользовательское соглашение", On: true, URL: "https://example.com/terms"},
		MenuButton{ID: "cmv58jrv6", Action: "url", Label: "🔒 Политика конфиденциальности", On: true, Row: true, URL: "https://example.com/privacy"},
		MenuButton{ID: "c9", Action: "page", Label: "📄 О сервисе", On: true, Text: "**{brand}** — быстрый VPN"},
		MenuButton{ID: "c10", Action: "url", Label: "Старое", On: false, URL: "https://example.com"},
	)
	return cfg
}

func TestWelcomeShowsTheDocuments(t *testing.T) {
	b := lookBot(t, nil)
	rows := b.welcomeButtons(exampleMenu())
	if len(rows) != 2 || len(rows[0]) != 2 || len(rows[1]) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0][0].URL != "https://example.com/terms" || rows[0][1].URL != "https://example.com/privacy" || rows[1][0].CallbackData != "p:c9" {
		t.Fatalf("rows %+v", rows)
	}
	// Kept off the welcome screen on the addon's page: gone there, the row closes up.
	hidden := lookBot(t, func(s *plus.Settings) { s.Welcome.Hide = []string{"cmv58ga30", "c9"} })
	rows = hidden.welcomeButtons(exampleMenu())
	if len(rows) != 1 || len(rows[0]) != 1 || !strings.Contains(rows[0][0].Text, "конфиденциальности") {
		t.Fatalf("hidden: %+v", rows)
	}
	// mikan's own bot (no plus) shows none, as before.
	if rows := (&Bot{}).welcomeButtons(exampleMenu()); rows != nil {
		t.Fatalf("mikan's bot: %+v", rows)
	}
}

func TestWelcomePageOpensWithoutASubscription(t *testing.T) {
	b := lookBot(t, nil)
	b.d.Backend = brandOnly{}
	w := wordsFor("ru")
	text, kb, ok := b.welcomePage(context.Background(), exampleMenu(), w, "p", "c9", "")
	if !ok || !strings.Contains(text, "<b>ExampleVPN</b>") || kb.InlineKeyboard[0][0].CallbackData != "m" {
		t.Fatalf("page: %v %q %+v", ok, text, kb)
	}
	if _, _, ok := b.welcomePage(context.Background(), exampleMenu(), w, "p", "nope", ""); ok {
		t.Fatal("an unknown page opened")
	}
	if _, _, ok := b.welcomePage(context.Background(), exampleMenu(), w, "s", "", ""); ok {
		t.Fatal("another press taken for a page")
	}
	hidden := lookBot(t, func(s *plus.Settings) { s.Welcome.Hide = []string{"c9"} })
	hidden.d.Backend = brandOnly{}
	if _, _, ok := hidden.welcomePage(context.Background(), exampleMenu(), w, "p", "c9", ""); ok {
		t.Fatal("a page kept off the welcome screen opened from it")
	}
}

// brandOnly is a backend that knows the brand and nothing else.
type brandOnly struct{ Backend }

func (brandOnly) Brand(context.Context) string { return "ExampleVPN" }
