package tgbot

import (
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
			*f.field(&cp) = v
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

// CheckTexts says what the page may not save: an unknown text, one too long, or a built-in
// one whose %s and %d are not the built-in's, as the bot fills them in that order.
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
		if got := verbs(v); !slices.Equal(got, want) {
			msg := "в тексте не должно быть %s и %d: бот ничего туда не подставляет"
			if len(want) > 0 {
				msg = "оставьте в тексте " + strings.Join(want, ", ") + " — по порядку, как в исходном: туда бот подставляет значения"
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
