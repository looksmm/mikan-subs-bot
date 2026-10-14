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

// Auto is the admin's automatic messages: their own rules, and the bot's own messages (a
// payment, a refund, the reminders) as the admin words them.
type Auto struct {
	Rules []Rule `json:"rules"`
	// Builtin: the bot's own messages by key (Builtins), as the admin set them.
	Builtin map[string]Builtin `json:"builtin,omitempty"`
	Saved   int64              `json:"saved"`
}

// Builtin is one of the bot's own messages: the standard one, the admin's, or none.
type Builtin struct {
	Mode    string  `json:"mode"` // "" the standard one, "own" the message below, "off" none
	Message Message `json:"message"`
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
	Replace bool `json:"replace,omitempty"`
	// Tariffs: only subscriptions of these tariffs (by name); none: any.
	Tariffs []string `json:"tariffs,omitempty"`
	Message Message  `json:"message"`
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
	{Key: "before_end", Name: "До конца подписки", Hint: "за заданное время до конца подписки — срок считает сам бот", Sub: true},
	{Key: "after_end", Name: "После конца подписки", Hint: "через заданное время после конца, если так и не продлил — срок считает сам бот", Sub: true},
	{Key: "expiring", Name: "Напоминание панели о конце", Hint: "когда панель напоминает: за 3 дня и за 1 день (если включено в «Настроить → Напоминания клиентам»)", Notice: true, Sub: true},
	{Key: "expired", Name: "Подписка закончилась (по панели)", Hint: "в день окончания, когда панель сообщает; с задержкой — если так и не продлил", Notice: true, Sub: true},
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

// BuiltinMsg is one of the bot's own messages, as the page lists them.
type BuiltinMsg struct {
	Key   string `json:"key"`
	Group string `json:"group"`
	Name  string `json:"name"`
	Hint  string `json:"hint"`
	// Vars are the variables its text may hold.
	Vars string `json:"vars"`
	// Panel: the standard text is the panel's (Настроить → Напоминания клиентам), which sends
	// it only when it is on there.
	Panel bool `json:"panel,omitempty"`
}

const (
	varsPaid   = "{name} {tariff} {amount} {until} {days} {link} {first_name} {brand}"
	varsRefund = "{name} {tariff} {amount} {until} {first_name} {brand}"
	varsSub    = "{name} {tariff} {until} {days} {traffic} {left} {link} {first_name} {brand}"
)

// Builtins are the bot's own messages, in the order the page lists them.
var Builtins = []BuiltinMsg{
	{Key: "paid_new", Group: "Оплата", Name: "Новая подписка оплачена", Hint: "сразу после оплаты новой подписки", Vars: varsPaid},
	{Key: "paid_renew", Group: "Оплата", Name: "Продление оплачено", Hint: "сразу после оплаты продления", Vars: varsPaid},
	{Key: "paid_package", Group: "Оплата", Name: "Трафик оплачен", Hint: "сразу после оплаты пакета трафика; {tariff} — пакет", Vars: varsPaid},
	{Key: "refunded_new", Group: "Возврат", Name: "Возврат новой подписки", Hint: "подписка отключена", Vars: varsRefund},
	{Key: "refunded_renew", Group: "Возврат", Name: "Возврат продления", Hint: "срок подписки уменьшен", Vars: varsRefund},
	{Key: "refunded_package", Group: "Возврат", Name: "Возврат пакета трафика", Hint: "{tariff} — пакет", Vars: varsRefund},
	{Key: "refunded_gone", Group: "Возврат", Name: "Возврат, подписки уже нет", Hint: "подписку удалили до возврата", Vars: "{tariff} {amount} {first_name} {brand}"},
	{Key: "trial_not_given", Group: "Возврат", Name: "Пробный период не выдан", Hint: "оплатил пробный, но выдать нельзя (уже брал); деньги вернут вручную", Vars: "{amount} {first_name} {brand}"},
	{Key: "expire_3d", Group: "Напоминания панели", Name: "Скоро конец: за 3 дня", Hint: "когда панель напоминает за 3 дня", Vars: varsSub, Panel: true},
	{Key: "expire_1d", Group: "Напоминания панели", Name: "Скоро конец: за 1 день", Hint: "когда панель напоминает за 1 день", Vars: varsSub, Panel: true},
	{Key: "expired", Group: "Напоминания панели", Name: "Подписка закончилась", Hint: "в день окончания", Vars: varsSub, Panel: true},
	{Key: "traffic_90", Group: "Напоминания панели", Name: "Трафик на исходе", Hint: "израсходовано 90%", Vars: varsSub, Panel: true},
	{Key: "traffic_100", Group: "Напоминания панели", Name: "Трафик закончился", Hint: "израсходован весь", Vars: varsSub, Panel: true},
}

// BuiltinOf is the bot's own message of key.
func BuiltinOf(key string) (BuiltinMsg, bool) {
	i := slices.IndexFunc(Builtins, func(b BuiltinMsg) bool { return b.Key == key })
	if i < 0 {
		return BuiltinMsg{}, false
	}
	return Builtins[i], true
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
	for k, b := range a.Builtin {
		b.Message.Normalize()
		if _, ok := BuiltinOf(k); !ok || b.Mode == "" {
			delete(a.Builtin, k) // the standard one needs no entry
			continue
		}
		a.Builtin[k] = b
	}
	if len(a.Builtin) == 0 {
		a.Builtin = nil
	}
	for i := range a.Rules {
		r := &a.Rules[i]
		r.ID, r.Name, r.Trigger = strings.TrimSpace(r.ID), strings.TrimSpace(r.Name), strings.TrimSpace(r.Trigger)
		r.Message.Normalize()
		var tariffs []string
		for _, t := range r.Tariffs {
			if t = strings.TrimSpace(t); t != "" && !slices.Contains(tariffs, t) {
				tariffs = append(tariffs, t)
			}
		}
		r.Tariffs = tariffs
		if tr, ok := TriggerOf(r.Trigger); ok && !tr.Sub {
			r.Tariffs = nil
		}
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
	for k, b := range a.Builtin {
		m, _ := BuiltinOf(k)
		switch b.Mode {
		case "off":
		case "own":
			if err := b.Message.Valid("builtin."+k+".message", "сообщение «"+m.Name+"»"); err != nil {
				return err
			}
		default:
			return &FieldError{"builtin." + k + ".mode", "сообщение «" + m.Name + "»: стандартное, своё или не отправлять"}
		}
	}
	ids := map[string]bool{}
	for i, r := range a.Rules {
		f := fmt.Sprintf("rules.%d", i)
		if len(r.Tariffs) > 20 {
			return &FieldError{f + ".tariffs", "не больше 20 тарифов в условии"}
		}
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
