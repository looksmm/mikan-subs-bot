package tgbot

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"subsbot/internal/plus"
	"subsbot/internal/tgate"
)

// lookBot is a bot whose look needs no panel: the settings are the defaults (or set), and
// what tells the buttons apart is given.
func lookBot(t *testing.T, set func(s *plus.Settings)) *Bot {
	t.Helper()
	st := plus.NewStore(plus.NewKV("http://127.0.0.1:1/panel/v2", "x", nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	// The ready set of icons, turned on (a fresh bot has it off).
	s := *st.Get()
	s.Look.On = true
	if set != nil {
		set(&s)
	}
	// Saving would call the panel: the settings go in as a load would put them.
	st.Use(s)
	b := &Bot{x: &ext{set: st, log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	b.x.lc = &lookCtx{at: time.Now(), w: wordsFor("ru"), urls: map[string]string{"https://t.me/plus_news": "c7"}, support: "https://t.me/plus_support"}
	return b
}

func dressOf(t *testing.T, b *Bot, method string, in any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(in)
	out, ok := b.dress(method, body)
	if !ok {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func buttonsOf(m map[string]any) []map[string]any {
	var out []map[string]any
	rows, _ := m["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	for _, r := range rows {
		for _, x := range r.([]any) {
			out = append(out, x.(map[string]any))
		}
	}
	return out
}

// mikan's main menu, as menu() builds it with the default buttons, gets the ready set's
// icons and colours; the plain emoji before the labels give way to the icons.
func TestLookDressesMikansMenu(t *testing.T) {
	b := lookBot(t, nil)
	w := wordsFor("ru")
	kb := &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{
		{{Text: "📊 Подписка", CallbackData: "s"}, {Text: "📱 Устройства", CallbackData: "d"}},
		{{Text: "🔌 Подключить устройство", CallbackData: "c"}},
		{{Text: "💳 Продлить", CallbackData: "r"}, {Text: "💬 Поддержка", URL: "https://t.me/plus_support"}},
		{{Text: "🌐 Открыть страницу подписки", WebApp: &tgate.WebApp{URL: "https://sub.example.com/s/tg"}}},
		{{Text: w.promo, WebApp: &tgate.WebApp{URL: "https://sub.example.com/s/tg#promocodes"}}},
		{{Text: "📰 Новости", URL: "https://t.me/plus_news"}},
		{{Text: w.back, CallbackData: "m"}},
	}}
	m := dressOf(t, b, "sendMessage", map[string]any{"chat_id": int64(9007199254740993), "text": "x", "parse_mode": "HTML", "reply_markup": kb})
	if m == nil {
		t.Fatal("nothing dressed")
	}
	def := plus.Default().Look.Icons
	want := []struct{ text, key string }{
		{"Подписка", "sub"}, {"Устройства", "devices"}, {"Подключить устройство", "connect"}, {"Продлить", "renew"},
		{"Поддержка", "support"}, {"Открыть страницу подписки", "app"}, {"Промокоды", "promo"}, {"📰 Новости", ""}, {"Назад", "back"},
	}
	btns := buttonsOf(m)
	if len(btns) != len(want) {
		t.Fatalf("%d buttons", len(btns))
	}
	for i, x := range want {
		got := btns[i]
		if got["text"] != x.text {
			t.Errorf("button %d: text %q, want %q", i, got["text"], x.text)
		}
		icon, _ := got["icon_custom_emoji_id"].(string)
		style, _ := got["style"].(string)
		if icon != def[x.key].ID || style != def[x.key].Style {
			t.Errorf("button %q: icon %q style %q, want %+v", x.text, icon, style, def[x.key])
		}
	}
	// The chat id goes back exactly as it came, past float64's precision.
	if n, _ := m["chat_id"].(float64); n == 0 {
		t.Fatalf("chat id %v", m["chat_id"])
	}
	body, _ := json.Marshal(map[string]any{"chat_id": int64(9007199254740993), "text": "x", "parse_mode": "HTML", "reply_markup": kb})
	out, _ := b.dress("sendMessage", body)
	if !strings.Contains(string(out), `"chat_id":9007199254740993`) {
		t.Fatalf("chat id changed: %s", out)
	}
}

func TestLookKeysOfOtherScreens(t *testing.T) {
	b := lookBot(t, nil)
	lc := b.x.lc
	w := lc.w
	for _, c := range []struct {
		btn tgate.Button
		key string
	}{
		{tgate.Button{Text: w.buy, CallbackData: "b"}, "buy"},
		{tgate.Button{Text: w.trial, CallbackData: "tr"}, "trial"},
		{tgate.Button{Text: "Стандарт · от 199 ₽", CallbackData: "t:3"}, "tariff"},
		{tgate.Button{Text: "1 месяц · 199 ₽", CallbackData: "t:3.30"}, "term"},
		{tgate.Button{Text: "1 месяц · 199 ₽", CallbackData: "tn:3.30"}, "term"},
		{tgate.Button{Text: "💳 Картой или СБП — 199 ₽", CallbackData: "py:3.30:y"}, "pay"},
		{tgate.Button{Text: "⭐ Telegram Stars — 100 ⭐", CallbackData: "tp:s"}, "pay"},
		{tgate.Button{Text: "Оплатить 199 ₽", URL: "https://pay.example/x"}, "payurl"},
		{tgate.Button{Text: w.buyTraffic, CallbackData: "x"}, "traffic"},
		{tgate.Button{Text: "50 ГБ · 99 ₽", CallbackData: "xk:4"}, "package"},
		{tgate.Button{Text: w.yesUnbind, CallbackData: "du:12"}, "confirm"},
		{tgate.Button{Text: w.cancel, CallbackData: "d"}, "cancel"},
		{tgate.Button{Text: w.transferDeny, CallbackData: "tx:1:2"}, "cancel"},
		{tgate.Button{Text: "❌ iPhone", CallbackData: "dc:5"}, "device"},
		{tgate.Button{Text: w.trialOpen, CallbackData: "m"}, "home"},
		{tgate.Button{Text: w.channelJoin, URL: "https://t.me/example"}, "channel"},
		{tgate.Button{Text: w.channelCheck, CallbackData: "gc"}, "check"},
		{tgate.Button{Text: "📄 Правила", CallbackData: "p:c2"}, "page:c2"},
		{tgate.Button{Text: "📰 Новости", URL: "https://t.me/plus_news"}, "btn:c7"},
		{tgate.Button{Text: "ℹ️ О сервисе", CallbackData: "ab"}, "about"},
		{tgate.Button{Text: "Что-то своё", URL: "https://example.com"}, ""},
	} {
		raw, _ := json.Marshal(c.btn)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if got := lc.key(m); got != c.key {
			t.Errorf("%q (%s%s): %q, want %q", c.btn.Text, c.btn.CallbackData, c.btn.URL, got, c.key)
		}
	}
}

func TestLookOffAndPlainTextsStayAsTheyAre(t *testing.T) {
	off := lookBot(t, func(s *plus.Settings) { s.Look.On = false })
	if m := dressOf(t, off, "sendMessage", map[string]any{"chat_id": 1, "text": "📅", "parse_mode": "HTML"}); m != nil {
		t.Fatalf("dressed while off: %v", m)
	}
	b := lookBot(t, nil)
	// No parse mode: the text goes as plain text, a tag would show as it is.
	if m := dressOf(t, b, "sendMessage", map[string]any{"chat_id": 1, "text": "📅 срок"}); m != nil {
		t.Fatalf("a plain text got tags: %v", m)
	}
	// Calls that carry no message are never touched.
	if m := dressOf(t, b, "answerCallbackQuery", map[string]any{"callback_query_id": "1", "text": "📅"}); m != nil {
		t.Fatal("answerCallbackQuery dressed")
	}
	// Without an id, an icon is only a colour, and the label keeps its emoji.
	col := lookBot(t, func(s *plus.Settings) { s.Look.Icons = map[string]plus.Icon{"sub": {Style: "danger"}} })
	m := dressOf(t, col, "editMessageReplyMarkup", map[string]any{"chat_id": 1, "message_id": 2,
		"reply_markup": tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: "📊 Подписка", CallbackData: "s"}}}}})
	btn := buttonsOf(m)[0]
	if btn["text"] != "📊 Подписка" || btn["style"] != "danger" || btn["icon_custom_emoji_id"] != nil {
		t.Fatalf("colour only: %v", btn)
	}
	// With "keep the emoji", the icon goes before the label as it is.
	keep := lookBot(t, func(s *plus.Settings) { s.Look.StripEmoji = false })
	m = dressOf(t, keep, "sendMessage", map[string]any{"chat_id": 1, "text": "x",
		"reply_markup": tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: "📊 Подписка", CallbackData: "s"}}}}})
	if btn := buttonsOf(m)[0]; btn["text"] != "📊 Подписка" || btn["icon_custom_emoji_id"] == nil {
		t.Fatalf("keep emoji: %v", btn)
	}
}

func TestEmojify(t *testing.T) {
	m := map[string]string{"📅": "111111", "📦": "222222", "⛔️": "333333", "👨‍💻": "444444", "👨": "555555"}
	for _, c := range []struct{ in, want string }{
		{"📅 до 9 ноября", `<tg-emoji emoji-id="111111">📅</tg-emoji> до 9 ноября`},
		{"<b>📦</b> 5 ГБ", `<b><tg-emoji emoji-id="222222">📦</tg-emoji></b> 5 ГБ`},
		// The variation selector goes inside with the emoji, whichever way it was written.
		{"⛔️ закончилась", `<tg-emoji emoji-id="333333">⛔️</tg-emoji> закончилась`},
		{"⛔ закончилась", `<tg-emoji emoji-id="333333">⛔</tg-emoji> закончилась`},
		// A sequence is taken whole, before the emoji it starts with.
		{"👨‍💻 поддержка", `<tg-emoji emoji-id="444444">👨‍💻</tg-emoji> поддержка`},
		// Code and a custom emoji already there stay as they are.
		{"<code>📅</code> и <pre>📦</pre>", "<code>📅</code> и <pre>📦</pre>"},
		{`<tg-emoji emoji-id="9">📅</tg-emoji>`, `<tg-emoji emoji-id="9">📅</tg-emoji>`},
		// A link keeps its address; its text is dressed.
		{`<a href="https://x.y/📅">📅</a>`, `<a href="https://x.y/📅"><tg-emoji emoji-id="111111">📅</tg-emoji></a>`},
		{"без эмодзи", "без эмодзи"},
		{"обрыв <b", "обрыв <b"},
	} {
		if got := Emojify(c.in, m); got != c.want {
			t.Errorf("%q:\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestStripLeadingEmoji(t *testing.T) {
	for in, want := range map[string]string{
		"📊 Подписка":           "Подписка",
		"⬅️ Назад":             "Назад",
		"👨‍👩‍👧 Семья":          "Семья",
		"1️⃣ Первый":           "1️⃣ Первый", // a keycap starts with a digit: kept
		"Подписка":             "Подписка",
		"🎁":                    "🎁",
		"⭐ Telegram Stars — 5": "Telegram Stars — 5",
	} {
		if got := StripLeadingEmoji(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
