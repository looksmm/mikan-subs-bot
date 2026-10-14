package tgbot

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"subsbot/internal/plus"
)

// withTexts is a bot whose words are the built-in ones with texts over them.
func withTexts(t *testing.T, texts map[string]string) *Bot {
	t.Helper()
	b := lookBot(t, func(s *plus.Settings) { s.Texts = texts })
	setWordsHook(b.x.words)
	t.Cleanup(func() { setWordsHook(nil) })
	return b
}

func TestTextsReplaceTheBuiltInOnes(t *testing.T) {
	b := withTexts(t, map[string]string{"back": "◀️ В меню", "subTitle": "Ваша подписка «%s»"})
	w := wordsFor("ru")
	if w.back != "◀️ В меню" || w.subTitle != "Ваша подписка «%s»" || w.cancel != ru.cancel {
		t.Fatalf("words %q %q %q", w.back, w.subTitle, w.cancel)
	}
	// The built-in words themselves stay as they were, and the English bot is mikan's.
	if ru.back != "⬅️ Назад" || wordsFor("en") != &en {
		t.Fatal("the built-in words changed")
	}
	// The same settings give the same words; new settings, new words.
	if wordsFor("ru") != w {
		t.Fatal("words made again for the same settings")
	}
	s := *b.x.set.Get()
	s.Texts = map[string]string{"back": "Назад"}
	b.x.set.Use(s)
	if wordsFor("ru").back != "Назад" || wordsFor("ru").subTitle != ru.subTitle {
		t.Fatalf("after a change: %q %q", wordsFor("ru").back, wordsFor("ru").subTitle)
	}
	// No texts: mikan's own words, the very same (linkWordsFor tells the language by it).
	s.Texts = map[string]string{}
	b.x.set.Use(s)
	if wordsFor("ru") != &ru {
		t.Fatal("no texts should be mikan's words")
	}
}

func TestCheckTexts(t *testing.T) {
	ok := map[string]string{
		"back": "Назад", "subTitle": "Подписка: %s", "unbindLeft": "Ещё %d из %d за %s", "sub.screen": "**{name}**\n{state}",
		"connectText": "Ссылка: <code>%s</code>",
	}
	if err := CheckTexts(&plus.Settings{Texts: ok}); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"subTitle":   "Подписка",           // the name is gone
		"back":       "Назад %s",           // a %s with nothing to fill it
		"unbindLeft": "Ещё %s из %d за %d", // the order of the kinds changed
		"nope":       "x",
		"sub.screen": strings.Repeat("я", 3001),
	} {
		var fe *plus.FieldError
		if err := CheckTexts(&plus.Settings{Texts: map[string]string{k: v}}); !errors.As(err, &fe) || fe.Field != "texts."+k {
			t.Errorf("%s=%q: %v", k, v, err)
		}
	}
	// "%%" is a percent sign, not a value.
	if err := CheckTexts(&plus.Settings{Texts: map[string]string{"back": "Назад 100%%"}}); err != nil {
		t.Fatal(err)
	}
}

func TestTextEntries(t *testing.T) {
	es := TextEntries()
	if es[0].Key != tplSub || es[0].Vars == "" || es[0].Base == "" {
		t.Fatalf("first %+v", es[0])
	}
	seen := map[string]bool{}
	all := map[string]string{}
	for _, e := range es {
		if seen[e.Key] || e.Title == "" || e.Group == "" {
			t.Errorf("entry %+v", e)
		}
		seen[e.Key] = true
		if e.Base != "" {
			all[e.Key] = e.Base
			continue
		}
		if e.Default == "" {
			t.Errorf("no built-in text: %+v", e)
		}
		all[e.Key] = e.Default
	}
	// Every built-in text and every template's start passes as it is.
	if err := CheckTexts(&plus.Settings{Texts: all}); err != nil {
		t.Fatal(err)
	}
	// The texts the panel sets are not offered twice.
	for _, k := range []string{"welcome", "main", "renew", "expiring", "expired", "traffic90", "trafficEnd"} {
		if slices.ContainsFunc(es, func(e plus.TextEntry) bool { return e.Key == k }) {
			t.Errorf("%s is the panel's text", k)
		}
	}
}

// Values by number: in any order, some left out — the refund message without the tariff.
func TestNumberedTexts(t *testing.T) {
	text := "↩️ Продление «%2» отменено, деньги вернутся. Новый срок: %3. 100%% честно."
	withTexts(t, map[string]string{"refundedRenew": text})
	w := wordsFor("ru")
	got := fmt.Sprintf(w.refundedRenew, "Стандарт", "Мой VPN", "1 ноября")
	if got != "↩️ Продление «Мой VPN» отменено, деньги вернутся. Новый срок: 1 ноября. 100% честно." {
		t.Fatalf("numbered: %q", got)
	}
	// The order as the built-in's still works as it did.
	withTexts(t, map[string]string{"refundedRenew": "Возврат: %s, %s, %s"})
	if got := fmt.Sprintf(wordsFor("ru").refundedRenew, "a", "b", "c"); got != "Возврат: a, b, c" {
		t.Fatalf("in order: %q", got)
	}
	for _, c := range []struct {
		name, text string
		ok         bool
	}{
		{"numbered", text, true},
		{"one kept", "Окончание: %3", true},
		{"twice", "%2 и снова %2", true},
		{"a number past the values", "Окончание: %4", false},
		{"numbers and %s together", "%1 и %s", false},
		{"one dropped without numbers", "Возврат: %s, %s", false},
		{"numbers where nothing is filled in", "Назад %1", false},
	} {
		key := "refundedRenew"
		if c.name == "numbers where nothing is filled in" {
			key = "back"
		}
		err := CheckTexts(&plus.Settings{Texts: map[string]string{key: c.text}})
		if (err == nil) != c.ok {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}
