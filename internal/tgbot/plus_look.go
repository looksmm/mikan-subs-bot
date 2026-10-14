package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"subsbot/internal/plus"
)

// The look: the icons and colours of the buttons and the custom emoji in the texts,
// put on whatever the bot sends (tgate.Client.Decorate), so mikan's screens stay as they are.

// ext is what the bot has on top of mikan's official one.
type ext struct {
	set *plus.Store
	log *slog.Logger

	mu      sync.Mutex
	lc      *lookCtx
	loading bool
	refused time.Time // when Telegram last turned a dressed body down, told once a minute
	refuse  string

	// The words with the admin's texts over them, and what they were made of.
	texts     *words
	textsOf   *plus.Settings
	textsBase *words

	// e: the admin's menu, the automatic messages, the chats (plus_tree.go).
	e Extras
	// auto: the automatic messages' own state (plus_auto.go).
	auto autoState
	// bc: the broadcasts' runner (plus_bcast.go).
	bc bcast
	// answered: the presses answered lately (plus_tree.go).
	answered answerSet
}

// Plus turns on the bot's additions: the look comes from set. It dresses the bot's
// client, so call it before the bot sends anything.
func (b *Bot) Plus(set *plus.Store, log *slog.Logger) {
	b.x = &ext{set: set, log: log}
	b.d.Client.Decorate(b.dress, b.x.onRefused)
	wordsHook = b.x.words
	set.Check(CheckTexts)
}

// LastRefused is what Telegram said the last time it turned down a dressed message, and
// when; "" when it never did.
func (b *Bot) LastRefused() (string, time.Time) {
	if b.x == nil {
		return "", time.Time{}
	}
	b.x.mu.Lock()
	defer b.x.mu.Unlock()
	return b.x.refuse, b.x.refused
}

func (x *ext) onRefused(method string, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if time.Since(x.refused) > time.Minute {
		x.log.Warn("telegram refused the look, sent without it", "method", method, "err", err)
	}
	x.refused, x.refuse = time.Now(), err.Error()
}

// lookCtx is what tells the buttons apart besides their callback data: the words of the
// bot's language, and where the admin's link buttons and the support lead.
type lookCtx struct {
	at      time.Time
	w       *words
	urls    map[string]string // link → the admin's menu button id
	support string
}

const lookCtxTTL = 30 * time.Second

// lookContext is the last lookCtx; the first one is read now, a stale one again in the
// background while the old one serves.
func (b *Bot) lookContext() *lookCtx {
	x := b.x
	x.mu.Lock()
	lc, stale := x.lc, x.lc == nil || time.Since(x.lc.at) > lookCtxTTL
	if lc != nil && stale && !x.loading {
		x.loading = true
		go func() {
			next := b.readLookCtx()
			x.mu.Lock()
			x.lc, x.loading = next, false
			x.mu.Unlock()
		}()
	}
	x.mu.Unlock()
	if lc == nil {
		lc = b.readLookCtx()
		x.mu.Lock()
		x.lc = lc
		x.mu.Unlock()
	}
	return lc
}

func (b *Bot) readLookCtx() *lookCtx {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := b.config(ctx)
	lc := &lookCtx{at: time.Now(), w: wordsFor(cfg.Lang), urls: map[string]string{}, support: b.supportURL(ctx)}
	for _, mb := range cfg.Buttons {
		if mb.Action == "url" && mb.URL != "" {
			lc.urls[mb.URL] = mb.ID
		}
	}
	return lc
}

// dressed are the methods whose body carries a text or buttons to dress.
var dressed = map[string]bool{"sendMessage": true, "editMessageText": true, "editMessageReplyMarkup": true,
	"sendPhoto": true, "editMessageCaption": true, "sendAnimation": true, "sendVideo": true}

// dress puts the look on a call's JSON body; ok is false when nothing changed.
func (b *Bot) dress(method string, body []byte) ([]byte, bool) {
	if b.x == nil || !dressed[method] {
		return nil, false
	}
	set := b.x.set.Get()
	if !set.Look.On {
		return nil, false
	}
	var in map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // chat and message ids stay exact
	if dec.Decode(&in) != nil {
		return nil, false
	}
	changed := false
	if rm, ok := in["reply_markup"].(map[string]any); ok && len(set.Look.Icons) > 0 {
		lc := b.lookContext()
		rows, _ := rm["inline_keyboard"].([]any)
		for _, r := range rows {
			row, _ := r.([]any)
			for _, x := range row {
				if btn, ok := x.(map[string]any); ok && dressButton(btn, lc.key(btn), set) {
					changed = true
				}
			}
		}
	}
	if pm, _ := in["parse_mode"].(string); pm == "HTML" && len(set.Look.Emoji) > 0 {
		for _, f := range []string{"text", "caption"} {
			if t, ok := in[f].(string); ok {
				if nt := Emojify(t, set.Look.Emoji); nt != t {
					in[f], changed = nt, true
				}
			}
		}
	}
	if !changed {
		return nil, false
	}
	out, err := json.Marshal(in)
	if err != nil {
		return nil, false
	}
	return out, true
}

// dressButton puts the icon and the colour of key on btn.
func dressButton(btn map[string]any, key string, set *plus.Settings) bool {
	ic, ok := set.Look.Icons[key]
	if key == "" || !ok {
		return false
	}
	changed := false
	// A button the admin dressed on its own (the menu's, a message's) keeps its look.
	_, ownIcon := btn["icon_custom_emoji_id"]
	_, ownStyle := btn["style"]
	if ownIcon {
		ic.ID = ""
	}
	if ownStyle {
		ic.Style = ""
	}
	if ic.ID != "" {
		btn["icon_custom_emoji_id"] = ic.ID
		changed = true
		if set.Look.StripEmoji {
			if t, _ := btn["text"].(string); t != "" {
				if s := StripLeadingEmoji(t); s != "" && s != t {
					btn["text"] = s
				}
			}
		}
	}
	if ic.Style != "" {
		btn["style"] = ic.Style
		changed = true
	}
	return changed
}

// codeKeys: the callback codes of mikan's screens, by the button they stand for.
var codeKeys = map[string]string{
	"m": "home", "s": "sub", "d": "devices", "c": "connect", "r": "renew", "w": "subs",
	"b": "buy", "tr": "trial", "fp": "free", "x": "traffic", "xu": "traffic", "xk": "package",
	"rl": "newlink", "rm": "remove", "rd": "confirm", "rn": "confirm", "du": "confirm", "ta": "confirm",
	"tx": "cancel", "dc": "device", "u": "subitem", "gc": "check",
	"py": "pay", "pn": "pay", "tp": "pay", "xp": "pay",
	aboutCode: "about",
}

// key is which of the buttons the page dresses btn is, "" none.
func (lc *lookCtx) key(btn map[string]any) string {
	text, _ := btn["text"].(string)
	w := lc.w
	switch text {
	case w.back:
		return "back"
	case w.cancel, w.transferDeny:
		return "cancel"
	case w.channelJoin:
		return "channel"
	case w.channelCheck:
		return "check"
	}
	if cb, ok := btn["callback_data"].(string); ok {
		// Under a message of the admin's: the button of the screen it opens.
		cb = strings.TrimPrefix(cb, freshCode+":")
		cmd, arg, _ := strings.Cut(cb, ":")
		switch cmd {
		case "p":
			return "page:" + arg
		case "t", "tn":
			// A tariff in the list, or one of its terms: <tariff>.<days>.
			if strings.Contains(arg, ".") {
				return "term"
			}
			return "tariff"
		}
		return codeKeys[cmd]
	}
	if u, ok := btn["url"].(string); ok {
		switch {
		case u == lc.support:
			return "support"
		case lc.urls[u] != "":
			return "btn:" + lc.urls[u]
		case strings.HasPrefix(text, strings.TrimSpace(strings.SplitN(w.payButton, "%", 2)[0])):
			return "payurl"
		}
		return ""
	}
	if wa, ok := btn["web_app"].(map[string]any); ok {
		if u, _ := wa["url"].(string); strings.HasSuffix(u, "#promocodes") {
			return "promo"
		}
		return "app"
	}
	return ""
}

// StripLeadingEmoji is a label without the emoji it starts with: "📊 Подписка" is
// "Подписка". A label that is all emoji stays as it is.
func StripLeadingEmoji(s string) string {
	i := 0
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if !isEmojiPart(r) {
			break
		}
		i += n
	}
	if i == 0 {
		return s
	}
	rest := strings.TrimLeftFunc(s[i:], unicode.IsSpace)
	if rest == "" {
		return s
	}
	return rest
}

// isEmojiPart: a rune emoji are made of — pictographs and symbols, and the joiners,
// variation selectors, skin tones and keycaps that go with them.
func isEmojiPart(r rune) bool {
	switch {
	case r == 0x200D, r == 0xFE0F, r == 0xFE0E, r == 0x20E3:
		return true
	case r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF, r >= 0x2B00 && r <= 0x2BFF,
		r >= 0x2300 && r <= 0x23FF, r >= 0x2190 && r <= 0x21FF, r >= 0x25A0 && r <= 0x25FF,
		r >= 0xE0020 && r <= 0xE007F, r == 0x00A9, r == 0x00AE, r == 0x203C, r == 0x2049, r == 0x2122, r == 0x2139:
		return true
	}
	return false
}

// Emojify puts the custom emoji of m in place of the plain ones in an HTML text: outside
// tags, code and the custom emoji already there. The plain emoji stays inside the tag, as
// Telegram shows it where a custom one cannot be shown.
func Emojify(s string, m map[string]string) string {
	if len(m) == 0 || s == "" {
		return s
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, strings.TrimSuffix(k, "️"))
	}
	// The longest first: a sequence before the emoji it starts with.
	slices.SortFunc(keys, func(a, b string) int { return len(b) - len(a) })
	id := func(k string) string {
		if v, ok := m[k]; ok {
			return v
		}
		return m[k+"️"]
	}
	var out strings.Builder
	skip := 0 // inside code, pre or tg-emoji
	for i := 0; i < len(s); {
		if s[i] == '<' {
			j := strings.IndexByte(s[i:], '>')
			if j < 0 {
				out.WriteString(s[i:])
				break
			}
			tag := s[i : i+j+1]
			name := ""
			if f := strings.Fields(strings.Trim(tag, "<>/")); len(f) > 0 {
				name = strings.ToLower(f[0])
			}
			if name == "code" || name == "pre" || name == "tg-emoji" {
				if strings.HasPrefix(tag, "</") {
					skip = max(0, skip-1)
				} else if !strings.HasSuffix(tag, "/>") {
					skip++
				}
			}
			out.WriteString(tag)
			i += j + 1
			continue
		}
		if skip == 0 {
			matched := false
			for _, k := range keys {
				if strings.HasPrefix(s[i:], k) {
					content := k
					if strings.HasPrefix(s[i+len(k):], "️") {
						content += "️"
					}
					out.WriteString(`<tg-emoji emoji-id="` + html.EscapeString(id(k)) + `">` + content + `</tg-emoji>`)
					i += len(content)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		_, n := utf8.DecodeRuneInString(s[i:])
		out.WriteString(s[i : i+n])
		i += n
	}
	return out.String()
}
