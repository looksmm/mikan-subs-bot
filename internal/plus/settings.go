// Package plus is what the bot has on top of mikan's official one: its settings (kept in the
// panel's store for addons, edited on the addon's page in the admin panel) and the clients
// of what it reads and writes there.
package plus

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Settings are the admin's, saved as one JSON value in the panel's store (key "settings").
type Settings struct {
	// Look: the icons and colours of the buttons and the custom emoji in the texts.
	Look Look `json:"look"`
	// Welcome: what the screen of those without a subscription shows besides mikan's.
	Welcome Welcome `json:"welcome"`
	// About: the «О сервисе» screen, which takes the documents out of the menu.
	About About `json:"about"`
	// Texts: the bot's own texts the admin changed, by key (see the page's list); a key
	// that is absent or empty is the built-in text.
	Texts map[string]string `json:"texts"`
	// TestChat is the admin's Telegram id: the page sends the messages there to check them.
	TestChat int64 `json:"test_chat,omitempty"`
	// Saved is when the page saved them last (unix seconds).
	Saved int64 `json:"saved"`
}

// Look is how the bot dresses what it sends.
type Look struct {
	On bool `json:"on"`
	// Icons by button (see Buttons): a custom emoji before the label and a colour.
	Icons map[string]Icon `json:"icons"`
	// Emoji: a plain emoji in the texts and the custom emoji that stands in for it.
	Emoji map[string]string `json:"emoji"`
	// StripEmoji drops the plain emoji at the start of a label that has an icon: the icon
	// takes its place.
	StripEmoji bool `json:"strip_emoji"`
}

// About is a screen of its own: a text about the service and the admin's
// link and page buttons (the privacy policy, the agreement), which leave the main menu and
// the welcome screen for it; its button takes their place, or sits before «Поддержка».
type About struct {
	On    bool        `json:"on"`
	Label string      `json:"label"` // the button; "" is DefaultAboutLabel
	Text  string      `json:"text"`  // Markdown, {brand}; "" is the built-in text
	Items []AboutItem `json:"items"`
	// WithSupport puts the button in the row of «Поддержка», before it.
	WithSupport bool `json:"with_support"`
}

// AboutItem is one of the admin's menu buttons shown on the screen, with a shorter label of
// its own ("" is the panel's).
type AboutItem struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// DefaultAboutLabel is the button's label when the admin set none.
const DefaultAboutLabel = "ℹ️ О сервисе"

// TextEntry is a text of the bot the page may change: its key in Settings.Texts, where it
// shows, the built-in text, and the variables of a template.
type TextEntry struct {
	Key     string `json:"key"`
	Group   string `json:"group"`
	Title   string `json:"title"`
	Default string `json:"default"`
	Vars    string `json:"vars,omitempty"`
	Hint    string `json:"hint,omitempty"`
	// Base is a template's start: mikan's screen written as the template.
	Base string `json:"base,omitempty"`
}

// Welcome: the admin's link and page buttons of the menu show on the welcome screen too
// (the user agreement, the privacy policy), but for those listed in Hide (their ids).
type Welcome struct {
	Hide []string `json:"hide"`
}

// Icon of a button: ID is the custom emoji's id ("" none), Style its colour.
type Icon struct {
	ID    string `json:"id"`
	Style string `json:"style"`
}

// Styles Telegram takes for a button: blue, green, red; "" is the app's own.
var Styles = []string{"", "primary", "success", "danger"}

// Button is a kind of button the admin can dress: Key is what the bot calls it, Name what
// the page shows.
type Button struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Hint string `json:"hint,omitempty"`
	// Off: an admin's menu button that is turned off in the panel.
	Off bool `json:"off,omitempty"`
}

// Buttons are the bot's own buttons, in the order the page lists them. The admin's menu
// buttons (links, pages) come after them as "btn:<id>" and "page:<id>".
var Buttons = []Button{
	bt("sub", "Подписка", "главное меню"),
	bt("devices", "Устройства", "главное меню"),
	bt("connect", "Подключить устройство", "главное меню"),
	bt("renew", "Продлить", "главное меню"),
	bt("support", "Поддержка", "главное меню и экраны покупки"),
	bt("app", "Страница подписки (Mini App)", "главное меню и экран подключения"),
	bt("promo", "Промокоды", "главное меню"),
	bt("subs", "Мои подписки", "когда их несколько"),
	bt("about", "О сервисе", "главное меню и первый экран"),
	bt("buy", "Купить подписку", "приветствие"),
	bt("trial", "Пробный период", "приветствие"),
	bt("free", "Бесплатный тариф", "приветствие и меню"),
	bt("tariff", "Тариф в списке", "покупка"),
	bt("term", "Срок тарифа", "покупка"),
	bt("pay", "Способ оплаты", "покупка"),
	bt("payurl", "Оплатить", "ссылка на оплату"),
	bt("traffic", "Докупить трафик", "подписка и уведомления"),
	bt("package", "Пакет трафика", "покупка трафика"),
	bt("newlink", "Новая ссылка", "подписка"),
	bt("remove", "Убрать из бота", "подписка"),
	bt("device", "Устройство в списке", "устройства"),
	bt("subitem", "Подписка в списке", "выбор подписки"),
	bt("confirm", "Подтвердить", "да, отвязать / убрать / разрешить"),
	bt("cancel", "Отмена", "отмена и отказ"),
	bt("back", "Назад", "на всех экранах"),
	bt("home", "В меню", "после покупки и пробного периода"),
	bt("channel", "Канал", "обязательная подписка"),
	bt("check", "Проверить", "обязательная подписка"),
}

func bt(key, name, hint string) Button { return Button{Key: key, Name: name, Hint: hint} }

// Default is a fresh bot's: the look off (custom emoji need the bot owner's Telegram Premium),
// with a ready set of icons and colours in it to turn on.
func Default() Settings {
	back := "5134598458514081480"
	return Settings{
		Look: Look{
			On:         false,
			StripEmoji: true,
			Icons: map[string]Icon{
				"sub":     {"5134263008683362081", "primary"},
				"devices": {"5134580350931962360", ""},
				"connect": {"5134484732075051047", "primary"},
				"renew":   {"5134483443584861960", "success"},
				"support": {"5134642413209390485", ""},
				"app":     {"5134484732075051047", ""},
				"about":   {"5134178204554102435", ""},
				"promo":   {"5134475154297980647", ""},
				"buy":     {"5134483443584861960", "success"},
				"trial":   {"5134602083466479299", "primary"},
				"pay":     {"5134569381585487347", ""},
				"payurl":  {"", "success"},
				"traffic": {"5134249123054094019", ""},
				"newlink": {"5134429430076147489", ""},
				"device":  {"5134460177747019082", ""},
				"confirm": {"5134557798058691685", ""},
				"back":    {back, ""},
				"home":    {back, ""},
			},
			Emoji: map[string]string{
				"📅": "5134197574856607298",
				"📦": "5134431040688883176",
				"📱": "5134231118551189370",
				"✅": "5134386300014560974",
				"❌": "5134520960124192408",
				"🎁": "5134475154297980647",
			},
		},
		Welcome: Welcome{Hide: []string{}},
		About:   About{Items: []AboutItem{}, WithSupport: true},
		Texts:   map[string]string{},
	}
}

var (
	emojiID  = regexp.MustCompile(`^[0-9]{5,25}$`)
	maxEmoji = 100
)

// FieldError is a setting the page sent that cannot be saved; Field names it for the page.
type FieldError struct{ Field, Message string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// Normalize trims what the page sent and drops what is empty.
func (s *Settings) Normalize() {
	if s.Look.Icons == nil {
		s.Look.Icons = map[string]Icon{}
	}
	for k, ic := range s.Look.Icons {
		ic.ID = strings.TrimSpace(ic.ID)
		// A button the bot no longer has (one saved before it went) is dropped, not refused:
		// the page sends what it loaded and must be able to save it.
		if ic.ID == "" && ic.Style == "" || !validKey(k) {
			delete(s.Look.Icons, k)
			continue
		}
		s.Look.Icons[k] = ic
	}
	if s.Look.Emoji == nil {
		s.Look.Emoji = map[string]string{}
	}
	for e, id := range s.Look.Emoji {
		e2, id2 := strings.TrimSpace(e), strings.TrimSpace(id)
		delete(s.Look.Emoji, e)
		if e2 != "" && id2 != "" {
			s.Look.Emoji[e2] = id2
		}
	}
	if s.Texts == nil {
		s.Texts = map[string]string{}
	}
	for k, v := range s.Texts {
		v = strings.TrimRight(strings.ReplaceAll(v, "\r\n", "\n"), " \t\n")
		if strings.TrimSpace(v) == "" {
			delete(s.Texts, k)
			continue
		}
		s.Texts[k] = v
	}
	s.About.Label = strings.TrimSpace(s.About.Label)
	s.About.Text = strings.TrimRight(strings.ReplaceAll(s.About.Text, "\r\n", "\n"), " \t\n")
	items := s.About.Items[:0:0]
	seenItem := map[string]bool{}
	for _, it := range s.About.Items {
		it.ID, it.Label = strings.TrimSpace(it.ID), strings.TrimSpace(it.Label)
		if it.ID != "" && !seenItem[it.ID] {
			seenItem[it.ID] = true
			items = append(items, it)
		}
	}
	if items == nil {
		items = []AboutItem{}
	}
	s.About.Items = items
	if s.Welcome.Hide == nil {
		s.Welcome.Hide = []string{}
	}
	slices.Sort(s.Welcome.Hide)
	s.Welcome.Hide = slices.Compact(s.Welcome.Hide)
}

// Valid says what the page may not save.
func (s *Settings) Valid() error {
	for k, ic := range s.Look.Icons {
		if !validKey(k) {
			return &FieldError{"look.icons", "неизвестная кнопка " + k}
		}
		if ic.ID != "" && !emojiID.MatchString(ic.ID) {
			return &FieldError{"look.icons." + k, "id эмодзи — это число из Telegram, например 5134263008683362081"}
		}
		if !slices.Contains(Styles, ic.Style) {
			return &FieldError{"look.icons." + k, "цвет: синий, зелёный, красный или обычный"}
		}
	}
	if len(s.Look.Emoji) > maxEmoji {
		return &FieldError{"look.emoji", fmt.Sprintf("не больше %d замен", maxEmoji)}
	}
	for e, id := range s.Look.Emoji {
		if utf8.RuneCountInString(e) > 8 || strings.ContainsAny(e, "<>&\"' \t\n") || isPlainText(e) {
			return &FieldError{"look.emoji", "заменять можно только эмодзи, не буквы и цифры: " + e}
		}
		if !emojiID.MatchString(id) {
			return &FieldError{"look.emoji", "id эмодзи для " + e + " — это число из Telegram"}
		}
	}
	if utf8.RuneCountInString(s.About.Label) > 40 {
		return &FieldError{"about.label", "надпись кнопки — не больше 40 символов"}
	}
	if utf8.RuneCountInString(s.About.Text) > 3000 {
		return &FieldError{"about.text", "текст — не больше 3000 символов"}
	}
	if len(s.About.Items) > 10 {
		return &FieldError{"about.items", "не больше 10 кнопок"}
	}
	for _, it := range s.About.Items {
		if len(it.ID) > 16 || strings.ContainsAny(it.ID, ": ") {
			return &FieldError{"about.items", "неизвестная кнопка " + it.ID}
		}
		if utf8.RuneCountInString(it.Label) > 40 {
			return &FieldError{"about.items", "надпись кнопки — не больше 40 символов"}
		}
	}
	if len(s.Welcome.Hide) > 40 {
		return &FieldError{"welcome.hide", "слишком много кнопок"}
	}
	for _, id := range s.Welcome.Hide {
		if id == "" || len(id) > 16 || strings.ContainsAny(id, ": ") {
			return &FieldError{"welcome.hide", "неизвестная кнопка " + id}
		}
	}
	if s.TestChat < 0 {
		return &FieldError{"test_chat", "Telegram ID — положительное число"}
	}
	return nil
}

// validKey: one of Buttons, or an admin's menu button of the panel.
func validKey(k string) bool {
	if strings.HasPrefix(k, "btn:") || strings.HasPrefix(k, "page:") {
		id := k[strings.IndexByte(k, ':')+1:]
		return id != "" && len(id) <= 16 && !strings.ContainsAny(id, ": ")
	}
	return slices.ContainsFunc(Buttons, func(b Button) bool { return b.Key == k })
}

// isPlainText: letters or digits, which a replacement would eat out of words and numbers.
func isPlainText(s string) bool {
	for _, r := range s {
		if r < 0x80 || (r >= 0x400 && r <= 0x4FF) {
			return true
		}
	}
	return false
}
