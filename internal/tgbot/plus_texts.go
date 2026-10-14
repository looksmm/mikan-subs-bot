package tgbot

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"subsbot/internal/plus"
)

// The admin's texts: they replace mikan's built-in words (plus_textlist.go) and the
// subscription screen on the addon's page; what the panel's Telegram page already sets (the
// welcome, the menu's header, renewal, the notices) stays there.

// textField is one built-in text the page may change: field points at it in a words.
type textField struct {
	key, group, title string
	field             func(w *words) *string
}

// maxText is how long one text may be: a message is up to 4096 characters.
const maxTextRunes = 3000

// wordsHook gives the words the bot speaks in: the built-in Russian ones with the admin's
// texts over them. nil is mikan's words as they are.
var wordsHook func(base *words) *words

// words is base with the texts of the settings over it, kept until the settings change.
func (x *ext) words(base *words) *words {
	s := x.set.Get()
	if len(s.Texts) == 0 {
		return base
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.textsOf == s && x.textsBase == base {
		return x.texts
	}
	cp := *base
	for _, f := range builtinTexts {
		if v := s.Texts[f.key]; v != "" {
			*f.field(&cp) = numbered(v, verbs(*f.field(base)))
		}
	}
	x.textsOf, x.textsBase, x.texts = s, base, &cp
	return &cp
}

// verbs are the printf verbs of a text, in order: what the bot fills in.
var verbRe = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

func verbs(s string) []string {
	var out []string
	for _, v := range verbRe.FindAllString(s, -1) {
		if v != "%%" {
			out = append(out, v)
		}
	}
	return out
}

// numRe is a value the admin names by its number: %1 is the first the bot fills in.
var numRe = regexp.MustCompile(`%%|%([1-9])`)

// numbers are the values a text names by number (%1, %3), in order; ok is false when the text
// uses none of them.
func numbers(s string) (nums []int, ok bool) {
	for _, m := range numRe.FindAllStringSubmatch(s, -1) {
		if m[1] != "" {
			nums = append(nums, int(m[1][0]-'0'))
		}
	}
	return nums, len(nums) > 0
}

// numbered turns a text with numbered values into the bot's: %2 becomes %[2]s (the verb of
// the built-in's second value), so the values may come in any order and some may be left out.
// A text in the built-in's order stays as it is.
func numbered(s string, want []string) string {
	if _, ok := numbers(s); !ok {
		return s
	}
	return numRe.ReplaceAllStringFunc(s, func(m string) string {
		if m == "%%" {
			return m
		}
		n := int(m[1] - '0')
		if n > len(want) {
			return m
		}
		return "%[" + m[1:] + "]" + strings.TrimPrefix(want[n-1], "%")
	})
}

// CheckTexts says what the page may not save: an unknown text, one too long, or a built-in
// one whose values are not the built-in's: either its %s and %d in their order, or values by
// number (%1, %2…), in any order, some of them left out but at least one kept.
func CheckTexts(s *plus.Settings) error {
	for k, v := range s.Texts {
		if utf8.RuneCountInString(v) > maxTextRunes {
			return &plus.FieldError{Field: "texts." + k, Message: "слишком длинный текст: не больше 3000 символов"}
		}
		if templateBase(k) != "" {
			continue
		}
		i := slices.IndexFunc(builtinTexts, func(f textField) bool { return f.key == k })
		if i < 0 {
			return &plus.FieldError{Field: "texts." + k, Message: "неизвестный текст " + k}
		}
		want := verbs(*builtinTexts[i].field(&ru))
		if nums, ok := numbers(v); ok {
			if err := checkNumbered(k, v, nums, len(want)); err != nil {
				return err
			}
			continue
		}
		if got := verbs(v); !slices.Equal(got, want) {
			msg := "в тексте не должно быть %s и %d: бот ничего туда не подставляет"
			if len(want) > 0 {
				msg = "оставьте в тексте " + strings.Join(want, ", ") + " по порядку, как в исходном, — или пишите значения номерами: %1, %2…, тогда лишние можно убрать"
			}
			return &plus.FieldError{Field: "texts." + k, Message: msg}
		}
	}
	return nil
}

// TextEntries are the texts the page lists: the screens' templates first, then the built-in
// words by group, each with its built-in text.
func TextEntries() []plus.TextEntry {
	var out []plus.TextEntry
	for _, t := range screenTemplates {
		out = append(out, plus.TextEntry{Key: t.key, Group: "Экраны бота целиком", Title: t.title, Vars: t.vars,
			Hint: strings.TrimSpace(t.hint + " " + lineHint), Base: t.base})
	}
	for _, f := range builtinTexts {
		out = append(out, plus.TextEntry{Key: f.key, Group: f.group, Title: f.title, Default: *f.field(&ru)})
	}
	return out
}

// checkNumbered checks a text whose values are named by number: each number one the bot
// fills in, no %s or %d beside them, at least one kept.
func checkNumbered(key, v string, nums []int, have int) error {
	field := "texts." + key
	if have == 0 {
		return &plus.FieldError{Field: field, Message: "в этот текст бот ничего не подставляет: номера вроде %1 здесь не нужны"}
	}
	for _, n := range nums {
		if n > have {
			return &plus.FieldError{Field: field, Message: fmt.Sprintf("в этом тексте значений %d: номер %%%d лишний", have, n)}
		}
	}
	if rest := verbs(numRe.ReplaceAllString(v, "")); len(rest) > 0 {
		return &plus.FieldError{Field: field, Message: "пишите значения либо номерами (%1, %2…), либо %s по порядку — не вперемешку"}
	}
	return nil
}
