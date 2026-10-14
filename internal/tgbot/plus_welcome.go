package tgbot

import (
	"context"
	"html"
	"slices"

	"subsbot/internal/tgate"
)

// The welcome screen: besides mikan's trial, purchase and support, the
// admin's own menu buttons (links and pages: the user agreement, the privacy policy), so
// that someone without a subscription sees them before paying.

// welcomeButtons are the admin's link and page buttons that are on in the menu and that the
// addon's settings do not keep off the welcome screen, laid out as in the menu.
func (b *Bot) welcomeButtons(cfg Config) [][]tgate.Button {
	if b.x == nil {
		return nil
	}
	hide := b.x.set.Get().Welcome.Hide
	var rows [][]tgate.Button
	for _, mb := range b.menuConfig(cfg).Buttons {
		if !mb.On || slices.Contains(hide, mb.ID) {
			continue
		}
		var btn tgate.Button
		switch mb.Action {
		case "url":
			btn = tgate.Button{Text: mb.Label, URL: mb.URL}
		case "page":
			btn = tgate.Button{Text: mb.Label, CallbackData: "p:" + mb.ID}
		case "about":
			btn = tgate.Button{Text: mb.Label, CallbackData: aboutCode}
		default:
			continue
		}
		if mb.Row && len(rows) > 0 && len(rows[len(rows)-1]) < 3 {
			rows[len(rows)-1] = append(rows[len(rows)-1], btn)
		} else {
			rows = append(rows, []tgate.Button{btn})
		}
	}
	return rows
}

// welcomePage is the admin's page a button of the welcome screen opens; shown is false for
// any other press.
func (b *Bot) welcomePage(ctx context.Context, cfg Config, w *words, cmd, arg, notice string) (string, *tgate.Keyboard, bool) {
	if b.x == nil || cmd != "p" {
		return "", nil, false
	}
	for _, mb := range cfg.Buttons {
		if mb.Action != "page" || mb.ID != arg || !mb.On || slices.Contains(b.x.set.Get().Welcome.Hide, mb.ID) && !b.aboutHas(mb.ID) {
			continue
		}
		text := render(mb.Text, map[string]string{"brand": b.brand(ctx)})
		if notice != "" {
			text = html.EscapeString(notice) + "\n\n" + text
		}
		return text, &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.back, CallbackData: "m"}}}}, true
	}
	return "", nil, false
}
