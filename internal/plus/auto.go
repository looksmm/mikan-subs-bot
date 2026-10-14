package plus

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Automatic messages: what the bot says by itself some time after something happened (the
// first /start, a payment, the end of a subscription…), with buttons into the menu. They are
// kept apart from the settings (key "auto").

// autoKey is where the automatic messages live in the panel's store.
const autoKey = "auto"

// Auto is the admin's automatic messages.
type Auto struct {
	Rules []Rule `json:"rules"`
	Saved int64  `json:"saved"`
}

// Rule is one automatic message: when it goes and what it says.
type Rule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	On      bool   `json:"on"`
	Trigger string `json:"trigger"`
	// Delay is minutes after the trigger; 0 at once.
	Delay int64 `json:"delay"`
	// Days of an expiring subscription: the panel tells 3 days and 1 day before; 0 both.
	Days int `json:"days,omitempty"`
	// First: after a payment, only the first purchase of a subscription.
	First bool `json:"first,omitempty"`
	// Replace: the panel's own notice of the same thing does not go (expiring, expired,
	// traffic).
	Replace bool    `json:"replace,omitempty"`
	Message Message `json:"message"`
	// Created is when the rule was made (unix): it acts on what happens after that, never
	// on the past.
	Created int64 `json:"created"`
}

// Message is what an automatic message or a broadcast says: a text (Markdown with
// variables), a picture by its address, and buttons.
type Message struct {
	Text  string     `json:"text"`
	Photo string     `json:"photo,omitempty"`
	Rows  [][]Action `json:"rows"`
}

// Trigger is one of the things an automatic message can follow, as the page offers them.
type Trigger struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Hint string `json:"hint"`
	// Notice: the panel has a notice of its own for it, which the message may replace.
	Notice bool `json:"notice,omitempty"`
	// Sub: the message is about a subscription ({name}, {until}, {days} are filled).
	Sub bool `json:"sub,omitempty"`
}

// Triggers, in the order the page lists them.
var Triggers = []Trigger{
	{Key: "start", Name: "Первый запуск бота", Hint: "через заданное время после первого /start"},
	{Key: "no_sub", Name: "Не оформил подписку", Hint: "через заданное время после первого /start, если подписки так и нет"},
	{Key: "paid", Name: "Оплата", Hint: "после оплаты подписки или продления", Sub: true},
	{Key: "expiring", Name: "Подписка скоро закончится", Hint: "когда панель напоминает: за 3 дня и за 1 день", Notice: true, Sub: true},
	{Key: "expired", Name: "Подписка закончилась", Hint: "в день окончания; с задержкой — если так и не продлил", Notice: true, Sub: true},
	{Key: "traffic_90", Name: "Трафик на исходе", Hint: "израсходовано 90% трафика", Notice: true, Sub: true},
	{Key: "traffic_100", Name: "Трафик закончился", Hint: "израсходован весь трафик", Notice: true, Sub: true},
}

// TriggerOf is the trigger of key.
func TriggerOf(key string) (Trigger, bool) {
	i := slices.IndexFunc(Triggers, func(t Trigger) bool { return t.Key == key })
	if i < 0 {
		return Trigger{}, false
	}
	return Triggers[i], true
}

// Limits of the automatic messages.
const (
	MaxRules   = 30
	MaxDelay   = 60 * 24 * 60 // minutes: sixty days
	MaxMessage = 4000         // Telegram's 4096 with room for the variables
)

var ruleID = regexp.MustCompile(`^[a-z0-9]{1,12}$`)

// DefaultAuto is no automatic messages.
func DefaultAuto() Auto { return Auto{Rules: []Rule{}} }

// Normalize trims what the page sent.
func (a *Auto) Normalize() {
	if a.Rules == nil {
		a.Rules = []Rule{}
	}
	for i := range a.Rules {
		r := &a.Rules[i]
		r.ID, r.Name, r.Trigger = strings.TrimSpace(r.ID), strings.TrimSpace(r.Name), strings.TrimSpace(r.Trigger)
		r.Message.Normalize()
		if r.Trigger != "expiring" {
			r.Days = 0
		}
		if r.Trigger != "paid" {
			r.First = false
		}
		if t, ok := TriggerOf(r.Trigger); ok && !t.Notice {
			r.Replace = false
		}
	}
}

// Normalize trims a message.
func (m *Message) Normalize() {
	m.Text, m.Photo = strings.TrimSpace(m.Text), strings.TrimSpace(m.Photo)
	m.Rows = normRows(m.Rows)
}

// Valid checks the automatic messages as the page saves them.
func (a *Auto) Valid() error {
	if len(a.Rules) > MaxRules {
		return &FieldError{"rules", fmt.Sprintf("автосообщений не больше %d", MaxRules)}
	}
	ids := map[string]bool{}
	for i, r := range a.Rules {
		f := fmt.Sprintf("rules.%d", i)
		where := "автосообщение «" + cmpOr(r.Name, r.ID) + "»"
		switch {
		case !ruleID.MatchString(r.ID):
			return &FieldError{f + ".id", "у автосообщения нет постоянного id"}
		case ids[r.ID]:
			return &FieldError{f + ".id", "два автосообщения с одним id"}
		case utf8.RuneCountInString(r.Name) > maxName:
			return &FieldError{f + ".name", fmt.Sprintf("название — до %d символов", maxName)}
		case r.Delay < 0 || r.Delay > MaxDelay:
			return &FieldError{f + ".delay", where + ": задержка — от нуля до 60 дней"}
		case r.Days != 0 && r.Days != 1 && r.Days != 3:
			return &FieldError{f + ".days", where + ": панель напоминает за 3 дня и за 1 день"}
		}
		if _, ok := TriggerOf(r.Trigger); !ok {
			return &FieldError{f + ".trigger", where + ": выберите, после чего оно приходит"}
		}
		if err := r.Message.Valid(f+".message", where); err != nil {
			return err
		}
		ids[r.ID] = true
	}
	return nil
}

// Valid checks a message: a text or a picture, the caption's bound under a picture, the
// buttons. A button's screen is looked up when it is sent: the menu may change after.
func (m *Message) Valid(field, where string) error {
	n := utf8.RuneCountInString(m.Text)
	switch {
	case m.Text == "" && m.Photo == "":
		return &FieldError{field + ".text", where + ": нужен текст или картинка"}
	case m.Photo != "" && !httpsURL(m.Photo):
		return &FieldError{field + ".photo", where + ": адрес картинки должен начинаться с https://"}
	case m.Photo != "" && n > MaxPhotoText:
		return &FieldError{field + ".text", fmt.Sprintf("%s: под картинкой текст — до %d символов", where, MaxPhotoText)}
	case n > MaxMessage:
		return &FieldError{field + ".text", fmt.Sprintf("%s: текст — до %d символов", where, MaxMessage)}
	}
	return validRows(field+".rows", m.Rows, nil, where)
}

func httpsURL(s string) bool {
	return strings.HasPrefix(s, "https://") && len(s) > len("https://") && len(s) <= 1024 && !strings.ContainsAny(s, " \n\t")
}

// Rule is the rule of id, if there is one.
func (a *Auto) Rule(id string) (Rule, bool) {
	for _, r := range a.Rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}
