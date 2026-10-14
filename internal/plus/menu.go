package plus

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"subsbot/internal/tgate"
)

// The admin's own menu: a tree of screens, each a text and rows of buttons, the buttons
// opening other screens or doing what the bot can do (the subscription, the purchase, a link…).
// It is kept apart from the settings (key "menu"), as the screens' texts make it large.

// menuKey is where the menu lives in the panel's store.
const menuKey = "menu"

// Menu is the tree. Off, or without a main screen, the bot shows mikan's menu (the panel's
// Telegram → Menu).
type Menu struct {
	On bool `json:"on"`
	// Main is the screen of the main menu, for those with a subscription.
	Main string `json:"main"`
	// Welcome is the first screen of those without a subscription; "" is mikan's (the trial,
	// the purchase, the support).
	Welcome string   `json:"welcome"`
	Screens []Screen `json:"screens"`
	Saved   int64    `json:"saved"`
}

// Screen is one screen of the tree.
type Screen struct {
	ID string `json:"id"`
	// Name is the admin's name of it: the page shows it, the bot never does.
	Name string `json:"name"`
	// Text is Markdown with variables; empty on the main menu is the panel's own text.
	Text string     `json:"text"`
	Rows [][]Action `json:"rows"`
	// Back adds a «Назад» row: to the screen it was opened from, else to the main menu.
	Back bool `json:"back"`
}

// Action is a button of the admin's: what it says, how it looks and what it does. The menu's
// screens, the automatic messages and the broadcasts are made of them.
type Action struct {
	Label string `json:"label"`
	Do    string `json:"do"`
	To    string `json:"to,omitempty"`    // do=screen: the screen's id
	URL   string `json:"url,omitempty"`   // do=url
	Icon  string `json:"icon,omitempty"`  // a custom emoji's id, before the label
	Style string `json:"style,omitempty"` // primary, success, danger: the button's colour
}

// Doing is one of the things a button can do, as the page offers them.
type Doing struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Hint string `json:"hint"`
	// Sub: the button shows only to those with a subscription.
	Sub bool `json:"sub,omitempty"`
}

// Doings are what a button can do, in the order the page lists them.
var Doings = []Doing{
	{Key: "screen", Name: "Открыть раздел", Hint: "другой экран этого меню"},
	{Key: "menu", Name: "Главное меню", Hint: "первый экран бота"},
	{Key: "sub", Name: "Моя подписка", Hint: "срок, трафик, устройства", Sub: true},
	{Key: "devices", Name: "Устройства", Hint: "список и отвязка", Sub: true},
	{Key: "connect", Name: "Подключить устройство", Hint: "ссылка подписки", Sub: true},
	{Key: "renew", Name: "Продлить", Hint: "тарифы для продления", Sub: true},
	{Key: "traffic", Name: "Докупить трафик", Hint: "если панель продаёт пакеты", Sub: true},
	{Key: "subs", Name: "Мои подписки", Hint: "когда их несколько", Sub: true},
	{Key: "buy", Name: "Купить подписку", Hint: "когда открыта покупка новой"},
	{Key: "trial", Name: "Пробный период", Hint: "пока его можно взять"},
	{Key: "free", Name: "Бесплатный тариф", Hint: "если он включён в панели"},
	{Key: "about", Name: "О сервисе", Hint: "экран «О сервисе»"},
	{Key: "support", Name: "Поддержка", Hint: "ссылка поддержки из панели"},
	{Key: "app", Name: "Страница подписки", Hint: "Mini App панели"},
	{Key: "promo", Name: "Промокоды", Hint: "Mini App, поле промокода"},
	{Key: "url", Name: "Ссылка", Hint: "адрес https:// или tg://"},
}

// Does says whether key is something a button can do.
func Does(key string) bool {
	return slices.ContainsFunc(Doings, func(d Doing) bool { return d.Key == key })
}

// Limits of the menu: Telegram's (64-byte callback data, 100 buttons, 4096-character
// messages) with room to spare, and a page that stays quick.
const (
	MaxScreens   = 60
	MaxRows      = 10
	MaxRowWidth  = 3
	MaxLabel     = 40
	MaxText      = 3500
	MaxPhotoText = 1000 // a caption under a picture
	maxName      = 60
)

var screenID = regexp.MustCompile(`^[a-z0-9]{1,12}$`)

// DefaultMenu is a fresh menu: off, nothing in it.
func DefaultMenu() Menu { return Menu{Screens: []Screen{}} }

// Normalize trims what the page sent and drops what cannot be a button: empty rows,
// buttons without a label and a doing.
func (m *Menu) Normalize() {
	if m.Screens == nil {
		m.Screens = []Screen{}
	}
	m.Main, m.Welcome = strings.TrimSpace(m.Main), strings.TrimSpace(m.Welcome)
	for i := range m.Screens {
		s := &m.Screens[i]
		s.ID = strings.TrimSpace(s.ID)
		s.Name = strings.TrimSpace(s.Name)
		s.Text = strings.TrimSpace(s.Text)
		s.Rows = normRows(s.Rows)
	}
}

func normRows(rows [][]Action) [][]Action {
	out := [][]Action{}
	for _, r := range rows {
		var row []Action
		for _, a := range r {
			a.Label, a.Do, a.To, a.URL = strings.TrimSpace(a.Label), strings.TrimSpace(a.Do), strings.TrimSpace(a.To), strings.TrimSpace(a.URL)
			a.Icon, a.Style = strings.TrimSpace(a.Icon), strings.TrimSpace(a.Style)
			if a.Label == "" && a.Do == "" {
				continue
			}
			if a.Do != "screen" {
				a.To = ""
			}
			if a.Do != "url" {
				a.URL = ""
			}
			row = append(row, a)
		}
		if len(row) > 0 {
			out = append(out, row)
		}
	}
	return out
}

// Valid checks the menu as the page saves it.
func (m *Menu) Valid() error {
	if len(m.Screens) > MaxScreens {
		return &FieldError{"screens", fmt.Sprintf("разделов не больше %d", MaxScreens)}
	}
	ids := map[string]bool{}
	for i, s := range m.Screens {
		f := fmt.Sprintf("screens.%d", i)
		switch {
		case !screenID.MatchString(s.ID):
			return &FieldError{f + ".id", "у раздела нет постоянного id (строчные латинские буквы и цифры)"}
		case ids[s.ID]:
			return &FieldError{f + ".id", "два раздела с одним id"}
		case utf8.RuneCountInString(s.Name) > maxName:
			return &FieldError{f + ".name", fmt.Sprintf("название раздела — до %d символов", maxName)}
		case utf8.RuneCountInString(s.Text) > MaxText:
			return &FieldError{f + ".text", fmt.Sprintf("текст раздела «%s» — до %d символов", s.Name, MaxText)}
		}
		ids[s.ID] = true
	}
	for i, s := range m.Screens {
		if err := validRows(fmt.Sprintf("screens.%d.rows", i), s.Rows, ids, "раздел «"+cmpOr(s.Name, s.ID)+"»"); err != nil {
			return err
		}
	}
	switch {
	case m.Main != "" && !ids[m.Main]:
		return &FieldError{"main", "главное меню указывает на раздел, которого нет"}
	case m.Welcome != "" && !ids[m.Welcome]:
		return &FieldError{"welcome", "первый экран указывает на раздел, которого нет"}
	case m.On && m.Main == "":
		return &FieldError{"main", "выберите раздел главного меню или выключите своё меню"}
	}
	return nil
}

// validRows checks rows of buttons; screens are the ids a button may open (nil: any, the
// menu is checked when it is drawn).
func validRows(field string, rows [][]Action, screens map[string]bool, where string) error {
	if len(rows) > MaxRows {
		return &FieldError{field, fmt.Sprintf("%s: рядов кнопок не больше %d", where, MaxRows)}
	}
	for i, r := range rows {
		if len(r) > MaxRowWidth {
			return &FieldError{fmt.Sprintf("%s.%d", field, i), fmt.Sprintf("%s: в ряду не больше %d кнопок", where, MaxRowWidth)}
		}
		for j, a := range r {
			f := fmt.Sprintf("%s.%d.%d", field, i, j)
			if err := a.valid(f, screens, where); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a Action) valid(f string, screens map[string]bool, where string) error {
	switch {
	case a.Label == "" || utf8.RuneCountInString(a.Label) > MaxLabel:
		return &FieldError{f + ".label", fmt.Sprintf("%s: у кнопки должна быть надпись до %d символов", where, MaxLabel)}
	case !Does(a.Do):
		return &FieldError{f + ".do", fmt.Sprintf("%s: кнопка «%s» ничего не делает", where, a.Label)}
	case a.Do == "screen" && a.To == "":
		return &FieldError{f + ".to", fmt.Sprintf("%s: кнопка «%s» не знает, какой раздел открыть", where, a.Label)}
	case a.Do == "screen" && screens != nil && !screens[a.To]:
		return &FieldError{f + ".to", fmt.Sprintf("%s: кнопка «%s» открывает раздел, которого нет", where, a.Label)}
	case a.Do == "url" && !tgate.SafeURL(a.URL):
		return &FieldError{f + ".url", fmt.Sprintf("%s: у кнопки «%s» адрес должен начинаться с https:// или tg://", where, a.Label)}
	case a.Icon != "" && !emojiID.MatchString(a.Icon):
		return &FieldError{f + ".icon", fmt.Sprintf("%s: иконка кнопки «%s» — это номер своего эмодзи (только цифры)", where, a.Label)}
	case !slices.Contains(Styles, a.Style):
		return &FieldError{f + ".style", fmt.Sprintf("%s: у кнопки «%s» нет такого цвета", where, a.Label)}
	}
	return nil
}

// Screen is the screen of id, if there is one.
func (m *Menu) Screen(id string) (Screen, bool) {
	for _, s := range m.Screens {
		if s.ID == id {
			return s, true
		}
	}
	return Screen{}, false
}

// Active: the bot draws this menu in place of mikan's.
func (m *Menu) Active() bool { return m != nil && m.On && m.Main != "" }

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
