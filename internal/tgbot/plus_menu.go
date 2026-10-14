package tgbot

import (
	"context"

	"subsbot/internal/plus"
)

// MenuButtons are the admin's own buttons of the main menu (links and pages, Telegram → Menu
// and texts in the panel), for the settings page to dress as well.
func (b *Bot) MenuButtons(ctx context.Context) []plus.Button {
	var out []plus.Button
	for _, mb := range b.config(ctx).Buttons {
		switch mb.Action {
		case "url":
			out = append(out, plus.Button{Key: "btn:" + mb.ID, Name: mb.Label, Hint: "ваша кнопка-ссылка", Off: !mb.On})
		case "page":
			out = append(out, plus.Button{Key: "page:" + mb.ID, Name: mb.Label, Hint: "ваша кнопка-страница", Off: !mb.On})
		}
	}
	return out
}
